package openai

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	sdk "github.com/openai/openai-go/v3"
)

func TestSDKRetries(t *testing.T) {
	for _, endpoint := range []string{"https://api.openai.com/v1", "https://example.openai.azure.com"} {
		for _, azureOptions := range []bool{false, true} {
			for _, retries := range []*int{nil, new(0), new(1)} {
				retryName := "default"
				if retries != nil {
					retryName = fmt.Sprint(*retries)
				}
				for _, status := range []int{429, 503} {
					name := fmt.Sprintf("%s/azure_options=%t/retries=%s/status=%d", endpoint, azureOptions, retryName, status)
					t.Run(name, func(t *testing.T) {
						calls := 0
						cfg := &Config{url: endpoint, token: "test", maxRetries: retries, client: &http.Client{Transport: responderLifecycleTransport(func(r *http.Request) (*http.Response, error) {
							calls++
							return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}, "Retry-After": {"0.001"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"busy"}}`)), Request: r}, nil
						})}}
						opts := cfg.Options()
						if azureOptions {
							opts = cfg.AzureOptions()
						}
						service := sdk.NewModelService(opts...)
						_, err := service.List(t.Context())
						wantCalls := 1
						if retries != nil {
							wantCalls += *retries
						}
						if err == nil || calls != wantCalls {
							t.Fatalf("calls=%d, want %d; error=%v", calls, wantCalls, err)
						}
					})
				}
			}
		}
	}
}

func TestSafetyIdentifierOnNativeEndpoints(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		native   bool
	}{
		{"https://api.openai.com/v1", true},
		{"https://deployment.openai.azure.com/openai/v1", true},
		{"https://proxy.invalid/providers/api.openai.com", false},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			opts := &provider.CompleteOptions{SafetyIdentifier: new("stable-user")}
			chat, err := NewCompleter(tc.endpoint, "gpt-6.2-sol", WithToken("fixture"))
			if err != nil {
				t.Fatal(err)
			}
			req, err := chat.convertCompletionRequest(nil, opts)
			if err != nil {
				t.Fatal(err)
			}
			responder, err := NewResponder(tc.endpoint, "gpt-6.2-sol", WithToken("fixture"))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := responder.convertResponsesRequest(nil, opts)
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if tc.native {
				want = *opts.SafetyIdentifier
			}
			if req.SafetyIdentifier.Value != want || resp.SafetyIdentifier.Value != want {
				t.Fatalf("safety identifier on Chat/Responses = %q/%q, want %q", req.SafetyIdentifier.Value, resp.SafetyIdentifier.Value, want)
			}
		})
	}
}
