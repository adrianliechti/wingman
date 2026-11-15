package openai

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

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
