package anthropic

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestSDKRetries(t *testing.T) {
	for _, endpoint := range []string{"https://api.anthropic.com", "https://bedrock-runtime.us-east-1.amazonaws.com", "https://bedrock-mantle.us-east-1.api.aws"} {
		for _, retries := range []*int{nil, new(0), new(1)} {
			retryName := "default"
			if retries != nil {
				retryName = fmt.Sprint(*retries)
			}
			for _, status := range []int{429, 503} {
				t.Run(fmt.Sprintf("%s/retries=%s/status=%d", endpoint, retryName, status), func(t *testing.T) {
					calls := 0
					opts := []Option{WithToken("test"), WithClient(&http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
						calls++
						return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}, "Retry-After": {"0.001"}}, Body: io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"api_error","message":"busy"}}`)), Request: r}, nil
					})})}
					wantCalls := 1
					if retries != nil {
						opts = append(opts, WithMaxRetries(*retries))
						wantCalls += *retries
					}
					c, err := NewCompleter(endpoint, "claude-test", opts...)
					if err != nil {
						t.Fatal(err)
					}
					var failure error
					for _, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("hello")}, nil) {
						failure = err
					}
					if calls != wantCalls || provider.CodeFromError(failure, 0) != status || provider.RetryAfterFromError(failure) != time.Millisecond {
						t.Fatalf("calls=%d, want %d; error=%v", calls, wantCalls, failure)
					}
				})
			}
		}
	}
}
