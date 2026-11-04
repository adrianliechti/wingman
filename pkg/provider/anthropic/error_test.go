package anthropic

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

func TestConvertError(t *testing.T) {
	for _, tc := range []struct {
		name, body, headerType string
		status                 int
		wantMessage, wantType  string
	}{
		{name: "Bedrock unavailable", status: 503, body: `{"message":"Bedrock is unable to process your request."}`, wantMessage: "Bedrock is unable to process your request."},
		{name: "Bedrock header", status: 429, body: `{"message":" Slow down "}`, headerType: "ThrottlingException:http://example.com", wantMessage: "Slow down", wantType: "ThrottlingException"},
		{name: "Bedrock type", status: 503, body: `{"__type":"com.amazon.bedrock#ServiceUnavailableException","message":"Unavailable"}`, wantMessage: "Unavailable", wantType: "ServiceUnavailableException"},
		{name: "Bedrock code", status: 403, body: `{"code":"AccessDeniedException","Message":"Denied"}`, wantMessage: "Denied", wantType: "AccessDeniedException"},
		{name: "Anthropic envelope", status: 429, body: `{"type":"error","error":{"type":"rate_limit_error","message":"Slow down"}}`, wantMessage: "Slow down", wantType: "rate_limit_error"},
		{name: "nested precedence", status: 503, body: `{"message":"outer","__type":"OuterError","error":{"type":"api_error","message":"inner"}}`, headerType: "HeaderError", wantMessage: "inner", wantType: "api_error"},
		{name: "HTTP status authoritative", status: 503, body: `{"error":{"type":"rate_limit_error","message":"Unavailable"}}`, wantMessage: "Unavailable", wantType: "rate_limit_error"},
		{name: "non JSON fallback", status: 502, body: "Bad gateway"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, "https://upstream.test/v1/messages", nil)
			if err != nil {
				t.Fatal(err)
			}
			apierr := &anthropic.Error{StatusCode: tc.status, Request: request, Response: &http.Response{
				StatusCode: tc.status, Header: http.Header{"Retry-After": {"2.25"}},
			}}
			apierr.Response.Header.Set("X-Amzn-Errortype", tc.headerType)
			if err := apierr.UnmarshalJSON([]byte(tc.body)); err != nil {
				t.Fatal(err)
			}
			original := fmt.Errorf("wrapped: %w", apierr)
			converted, ok := errors.AsType[*provider.ProviderError](convertError(original))
			if !ok {
				t.Fatal("missing provider error")
			}
			wantMessage := tc.wantMessage
			if wantMessage == "" {
				wantMessage = apierr.Error()
			}
			if converted.Code != tc.status || converted.Message != wantMessage || converted.Type != tc.wantType {
				t.Fatalf("got %+v", *converted)
			}
			if converted.RetryAfter != 2250*time.Millisecond || !errors.Is(converted, original) {
				t.Fatal("lost retry advice or original error")
			}
		})
	}
	plain := errors.New("transport failed")
	if convertError(nil) != nil || convertError(plain) != plain {
		t.Fatal("non-API errors must be unchanged")
	}
}

// Exercise the SDK's SSE decoder: these errors carry HTTP 200 even though
// their event types describe a failed request.
func TestStreamingErrorStatus(t *testing.T) {
	for _, tc := range []struct {
		typ  string
		code int
	}{
		{"invalid_request_error", 400}, {"authentication_error", 401},
		{"billing_error", 402}, {"permission_error", 403}, {"not_found_error", 404},
		{"conflict_error", 409}, {"request_too_large", 413},
		{"rate_limit_error", 429}, {"overloaded_error", 529},
		{"timeout_error", 504}, {"api_error", 502},
		{"throttlingException", 429}, {"serviceUnavailableException", 503},
		{"validationException", 400}, {"internalServerException", 502},
		{"modelTimeoutException", 504}, {"modelStreamErrorException", 502},
		{"future_error", 502}, {"", 502},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{"type": "error", "error": map[string]string{"type": tc.typ, "message": "Request failed"}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = accumulateStream(t, []string{string(payload)}, nil)
			if provider.CodeFromError(err, 0) != tc.code || provider.TypeFromError(err) != tc.typ || err.Error() != "Request failed" {
				t.Fatalf("status=%d type=%q error=%v", provider.CodeFromError(err, 0), provider.TypeFromError(err), err)
			}
		})
	}
}
