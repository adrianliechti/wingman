package google

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestDefaultNoRetries(t *testing.T) {
	for _, kind := range []string{"interactions", "embeddings", "decisions"} {
		for _, status := range []int{0, 429, 503} {
			t.Run(fmt.Sprintf("%s/status=%d", kind, status), func(t *testing.T) {
				calls := 0
				client := &http.Client{Transport: interactionTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if status == 0 {
						return nil, errors.New("connection failed")
					}
					body := fmt.Sprintf(`{"error":{"code":%d,"message":"busy"}}`, status)
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}, "Retry-After": {"0.001"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})}
				opts := []Option{WithToken("test"), WithClient(client)}
				var failure error
				switch kind {
				case "interactions":
					c, err := NewCompleter("gemini-3.8-flash", opts...)
					if err != nil {
						t.Fatal(err)
					}
					for _, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("hello")}, nil) {
						failure = err
					}
				case "embeddings":
					e, err := NewEmbedder("gemini-embedding-2", opts...)
					if err != nil {
						t.Fatal(err)
					}
					_, failure = e.Embed(t.Context(), []string{"hello"}, nil)
				case "decisions":
					d, err := NewDecider("", "gemini-decision-1", opts...)
					if err != nil {
						t.Fatal(err)
					}
					_, failure = d.Decide(t.Context(), geminiDecisionInput(t))
				}
				if failure == nil || calls != 1 {
					t.Fatalf("calls=%d, want 1; error=%v", calls, failure)
				}
				if status != 0 && provider.CodeFromError(failure, 0) != status {
					t.Fatalf("upstream status lost: %v", failure)
				}
			})
		}
	}
}
