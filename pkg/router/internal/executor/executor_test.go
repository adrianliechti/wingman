package executor

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type completerFunc func(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error]

func (f completerFunc) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return f(ctx, messages, options)
}

func roleOnly() *provider.Completion {
	return &provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant}}
}

func textCompletion(value string) *provider.Completion {
	return &provider.Completion{Message: &provider.Message{
		Role:    provider.MessageRoleAssistant,
		Content: []provider.Content{{Text: value}},
	}}
}

func TestAttemptBuffersPreludeUntilContent(t *testing.T) {
	streamErr := errors.New("stream failed")
	broken := completerFunc(func(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
		return func(yield func(*provider.Completion, error) bool) {
			yield(roleOnly(), nil)
			yield(nil, streamErr)
		}
	})

	var yielded int
	result := Attempt(context.Background(), broken, nil, nil, 0, func(*provider.Completion, error) bool {
		yielded++
		return true
	})

	if result.Delivered || result.Failure != FailureProvider || !errors.Is(result.Err, streamErr) {
		t.Fatalf("unexpected result: %+v", result)
	}
	if yielded != 0 {
		t.Fatalf("uncommitted prelude leaked to caller: %d chunks", yielded)
	}
}

func TestAttemptFlushesPreludeWhenContentArrives(t *testing.T) {
	stream := completerFunc(func(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
		return func(yield func(*provider.Completion, error) bool) {
			yield(roleOnly(), nil)
			yield(textCompletion("hello"), nil)
		}
	})

	var chunks []*provider.Completion
	result := Attempt(context.Background(), stream, nil, nil, time.Second, func(completion *provider.Completion, err error) bool {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		chunks = append(chunks, completion)
		return true
	})

	if !result.Delivered || result.Failure != FailureNone || result.TTFT <= 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(chunks) != 2 || len(chunks[0].Message.Content) != 0 || chunks[1].Message.Text() != "hello" {
		t.Fatalf("prelude/content order not preserved: %+v", chunks)
	}
}

func TestAttemptClassifiesFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want FailureKind
	}{
		{name: "request", err: &provider.ProviderError{Code: 400, Message: "invalid"}, want: FailureRequest},
		{name: "auth is provider-specific", err: &provider.ProviderError{Code: 401, Message: "auth"}, want: FailureProvider},
		{name: "rate limit is provider-specific", err: &provider.ProviderError{Code: 429, Message: "limited"}, want: FailureProvider},
		{name: "provider", err: errors.New("unavailable"), want: FailureProvider},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := completerFunc(func(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
				return func(yield func(*provider.Completion, error) bool) { yield(nil, test.err) }
			})

			result := Attempt(context.Background(), c, nil, nil, 0, func(*provider.Completion, error) bool { return true })
			if result.Failure != test.want || !errors.Is(result.Err, test.err) {
				t.Fatalf("got %+v, want %s", result, test.want)
			}
		})
	}
}

func TestAttemptFirstTokenTimeout(t *testing.T) {
	blocked := completerFunc(func(ctx context.Context, _ []provider.Message, _ *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
		return func(yield func(*provider.Completion, error) bool) {
			<-ctx.Done()
			yield(nil, ctx.Err())
		}
	})

	result := Attempt(context.Background(), blocked, nil, nil, 10*time.Millisecond, func(*provider.Completion, error) bool { return true })
	if result.Delivered || result.Failure != FailureTimeout || provider.CodeFromError(result.Err, 0) != 504 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestAttemptCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	called := false
	c := completerFunc(func(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
		called = true
		return func(func(*provider.Completion, error) bool) {}
	})

	result := Attempt(ctx, c, nil, nil, time.Second, func(*provider.Completion, error) bool { return true })
	if result.Failure != FailureCaller || !errors.Is(result.Err, context.Canceled) || called {
		t.Fatalf("unexpected result: %+v called=%v", result, called)
	}
}

func TestCompleteRetriesOnlyRetryableFailures(t *testing.T) {
	for _, test := range []struct {
		name        string
		firstErr    error
		wantOutput  string
		wantCalls   int
		wantLastErr error
	}{
		{name: "provider", firstErr: errors.New("down"), wantOutput: "ok", wantCalls: 1},
		{name: "request", firstErr: &provider.ProviderError{Code: 400, Message: "invalid"}, wantCalls: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			first := completerFunc(func(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
				return func(yield func(*provider.Completion, error) bool) { yield(nil, test.firstErr) }
			})
			secondCalls := 0
			second := completerFunc(func(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
				return func(yield func(*provider.Completion, error) bool) {
					secondCalls++
					yield(textCompletion("ok"), nil)
				}
			})

			var output string
			var lastErr error
			for completion, err := range Complete(context.Background(), []Candidate{{ID: "first", Completer: first}, {ID: "second", Completer: second}}, nil, nil, 0, Hooks{}) {
				if err != nil {
					lastErr = err
				}
				if completion != nil && completion.Message != nil {
					output += completion.Message.Text()
				}
			}

			if output != test.wantOutput || secondCalls != test.wantCalls {
				t.Fatalf("output=%q second calls=%d", output, secondCalls)
			}
			if test.wantOutput == "" && !errors.Is(lastErr, test.firstErr) {
				t.Fatalf("expected request error, got %v", lastErr)
			}
		})
	}
}
