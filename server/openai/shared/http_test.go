package shared

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3"
)

func TestWriteErrorPreservesNativeFields(t *testing.T) {
	for _, raw := range []string{
		`{"message":"tools unavailable","type":"invalid_request_error","param":"reasoning_effort","code":null}`,
		`{"message":"quota exceeded","type":"insufficient_quota","param":null,"code":"insufficient_quota","details":{"limit":9007199254740993}}`,
	} {
		var upstream openai.Error
		if err := json.Unmarshal([]byte(raw), &upstream); err != nil {
			t.Fatal(err)
		}
		err := &provider.ProviderError{
			Kind: provider.ErrorKindInvalidArgument, Type: upstream.Type, Message: upstream.Message,
			Vendor: provider.VendorOpenAI, RawJSON: upstream.RawJSON(), Err: &upstream, RetryAfter: 2 * time.Second,
		}
		rec := httptest.NewRecorder()
		WriteError(rec, http.StatusInternalServerError, err)
		if rec.Code != http.StatusBadRequest || rec.Header().Get("Retry-After") != "2" || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("error status/headers lost: %d %v", rec.Code, rec.Header())
		}
		var body struct {
			Error jsontext.Value `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if string(body.Error) != raw {
			t.Fatalf("error fields changed: got %s, want %s", body.Error, raw)
		}
	}
}

func TestWriteJSONRejectsInvalidOutputBeforeHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJson(rec, map[string]any{"prefix": "valid", "invalid": string([]byte{0xff})})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var response ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("partial response: %s (%v)", rec.Body.String(), err)
	}
	if response.Error.Type != "server_error" {
		t.Fatalf("wrong error: %+v", response)
	}
}

func TestWriteErrorRejectsInvalidUTF8BeforeHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusBadRequest, errors.New(string([]byte{0xff})))
	if rec.Code != http.StatusInternalServerError || !jsontext.Value(rec.Body.Bytes()).IsValid() {
		t.Fatalf("invalid fallback: %d %s", rec.Code, rec.Body.String())
	}
}

// OpenAI clients read error.code to decide whether to retry or compact. Codes
// from another vendor's vocabulary would mislead them; the kind supplies one.
func TestWriteErrorCodeAcrossVendors(t *testing.T) {
	for _, tc := range []struct {
		name          string
		err           *provider.ProviderError
		status        int
		errType, code string
	}{
		{"openai code", &provider.ProviderError{Kind: provider.ErrorKindInvalidArgument, Type: "invalid_function_parameters", Vendor: provider.VendorOpenAI, Message: "bad schema"}, 400, "invalid_request_error", "invalid_function_parameters"},
		{"foreign code omitted", &provider.ProviderError{Kind: provider.ErrorKindInvalidArgument, Type: "invalid_request_error", Vendor: provider.VendorAnthropic, Message: "bad schema"}, 400, "invalid_request_error", ""},
		{"wingman code kept", &provider.ProviderError{Kind: provider.ErrorKindInvalidArgument, Type: "invalid_value", Message: "bad value"}, 400, "invalid_request_error", "invalid_value"},
		{"gemini daily quota", &provider.ProviderError{Kind: provider.ErrorKindQuotaExceeded, Type: "RESOURCE_EXHAUSTED", Vendor: provider.VendorGoogle, Message: "quota"}, 429, "insufficient_quota", "insufficient_quota"},
		{"anthropic billing", &provider.ProviderError{Kind: provider.ErrorKindForStatus(402), Type: "billing_error", Vendor: provider.VendorAnthropic, Message: "credits exhausted"}, 429, "insufficient_quota", "insufficient_quota"},
		{"budget", &provider.ProviderError{Kind: provider.ErrorKindQuotaExceeded, Type: "insufficient_quota", Message: "budget"}, 429, "insufficient_quota", "insufficient_quota"},
		{"bedrock throttling", &provider.ProviderError{Kind: provider.ErrorKindRateLimited, Type: "throttlingException", Vendor: provider.VendorBedrock, Message: "slow down"}, 429, "rate_limit_exceeded", "rate_limit_exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteError(rec, http.StatusBadGateway, tc.err)
			if got := rec.Header().Get("x-should-retry"); (got == "false") != (tc.errType == "insufficient_quota") {
				t.Errorf("incorrect SDK retry advice: %q", got)
			}
			var body ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if rec.Code != tc.status || body.Error.Type != tc.errType || body.Error.Code != tc.code || ErrorTypeFromError(tc.err) != tc.errType || ErrorCodeFromError(tc.err) != tc.code {
				t.Fatalf("HTTP %d %+v", rec.Code, body.Error)
			}
		})
	}
}
