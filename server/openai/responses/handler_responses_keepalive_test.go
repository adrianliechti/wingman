package responses

import (
	"context"
	"errors"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
)

type keepaliveCompleter func(context.Context, func(*provider.Completion, error) bool)

func (f keepaliveCompleter) Complete(ctx context.Context, _ []provider.Message, _ *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) { f(ctx, yield) }
}

// Simulate a downstream proxy closing an idle connection after 45 seconds.
type idleStreamWriter struct {
	*httptest.ResponseRecorder
	lastWrite    time.Time
	disconnected bool
}

func (w *idleStreamWriter) Write(p []byte) (int, error) {
	if !w.lastWrite.IsZero() && time.Since(w.lastWrite) >= 45*time.Second {
		w.disconnected = true
	}
	if w.disconnected {
		return 0, io.ErrClosedPipe
	}
	w.lastWrite = time.Now()
	return w.ResponseRecorder.Write(p)
}

func serveKeepaliveTest(w http.ResponseWriter, c provider.Completer) {
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter("test", c)
	New(cfg).handleResponses(w, httptest.NewRequest("POST", "/responses", strings.NewReader(`{"model":"test","stream":true,"input":"Go"}`)))
}

func TestResponsesKeepsStreamAliveAfterCommentary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := &idleStreamWriter{ResponseRecorder: httptest.NewRecorder()}
		serveKeepaliveTest(w, keepaliveCompleter(func(ctx context.Context, yield func(*provider.Completion, error) bool) {
			if !yield(&provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{{Text: "Checking.", Phase: provider.MessagePhaseCommentary}}}}, nil) {
				return
			}
			time.Sleep(90 * time.Second)
			yield(&provider.Completion{Status: provider.CompletionStatusCompleted}, nil)
		}))
		if w.disconnected {
			t.Fatal("proxy disconnected during the pause after commentary")
		}
		if !strings.Contains(w.Body.String(), "event: response.completed\n") {
			t.Fatal("missing terminal completion")
		}
		if got := strings.Count(w.Body.String(), ": keep-alive\n\n"); got < 5 {
			t.Fatalf("got %d keepalives during 90s pause", got)
		}
		before := w.Body.String()
		time.Sleep(30 * time.Second)
		if w.Body.String() != before {
			t.Fatal("keepalive wrote after the handler returned")
		}
	})
}

func TestResponsesPreservesErrorsBeforeStreaming(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := httptest.NewRecorder()
		serveKeepaliveTest(w, keepaliveCompleter(func(_ context.Context, yield func(*provider.Completion, error) bool) {
			time.Sleep(time.Minute)
			yield(nil, &provider.ProviderError{Kind: provider.ErrorKindRateLimited, Message: "rate limited"})
		}))
		if w.Code != http.StatusTooManyRequests || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), ": keep-alive") {
			t.Fatal("keepalive committed a successful response before provider validation")
		}
	})
}

func TestResponsesReportsFailureAfterCommentary(t *testing.T) {
	w := httptest.NewRecorder()
	serveKeepaliveTest(w, keepaliveCompleter(func(_ context.Context, yield func(*provider.Completion, error) bool) {
		if !yield(&provider.Completion{Message: &provider.Message{Content: []provider.Content{{Text: "Checking."}}}}, nil) {
			return
		}
		yield(nil, errors.New("bedrock: stream ended without messageStop"))
	}))
	body := w.Body.String()
	if !strings.Contains(body, "event: error\n") || !strings.Contains(body, "event: response.failed\n") || strings.Contains(body, "event: response.completed\n") {
		t.Fatalf("failure lost: %s", body)
	}
}

type failedKeepaliveWriter struct {
	*httptest.ResponseRecorder
	failFlush bool
	closed    bool
}

func (w *failedKeepaliveWriter) Write(p []byte) (int, error) {
	if strings.HasPrefix(string(p), ": keep-alive") {
		w.closed = true
		if !w.failFlush {
			return 0, io.ErrClosedPipe
		}
	}
	return w.ResponseRecorder.Write(p)
}

func (w *failedKeepaliveWriter) FlushError() error {
	if w.closed && w.failFlush {
		return io.ErrClosedPipe
	}
	w.ResponseRecorder.Flush()
	return nil
}

func TestResponsesCancelsProviderWhenKeepaliveFails(t *testing.T) {
	for _, failFlush := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			w := &failedKeepaliveWriter{ResponseRecorder: httptest.NewRecorder(), failFlush: failFlush}
			cancelled := false
			serveKeepaliveTest(w, keepaliveCompleter(func(ctx context.Context, yield func(*provider.Completion, error) bool) {
				if !yield(&provider.Completion{Message: &provider.Message{Content: []provider.Content{{Text: "Checking."}}}}, nil) {
					return
				}
				select {
				case <-ctx.Done():
					cancelled = true
					yield(nil, ctx.Err())
				case <-time.After(time.Minute):
					t.Error("disconnected client left provider running")
				}
			}))
			if !cancelled {
				t.Fatal("provider was not cancelled")
			}
		})
	}
}
