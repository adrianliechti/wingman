package bedrock

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestDefaultNoRetries(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials"))
	t.Setenv("AWS_MAX_ATTEMPTS", "5")
	for _, kind := range []string{"completer", "realtime"} {
		for _, status := range []int{429, 503} {
			t.Run(fmt.Sprintf("%s/status=%d", kind, status), func(t *testing.T) {
				calls := 0
				client := &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}, "X-Amzn-Errortype": {"ThrottlingException"}}, Body: io.NopCloser(strings.NewReader(`{"message":"busy"}`)), Request: r}, nil
				})}
				var failure error
				if kind == "completer" {
					c, err := NewCompleter("amazon.nova-pro-v1:0", WithClient(client))
					if err != nil {
						t.Fatal(err)
					}
					for _, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("hello")}, nil) {
						failure = err
					}
				} else {
					r, err := NewRealtime("amazon.nova-2-sonic-v1:0", WithClient(client))
					if err != nil {
						t.Fatal(err)
					}
					_, failure = r.Connect(t.Context(), nil)
				}
				if failure == nil || calls != 1 {
					t.Fatalf("calls=%d, want 1; error=%v", calls, failure)
				}
			})
		}
	}
}
