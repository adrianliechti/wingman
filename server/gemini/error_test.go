package gemini

import (
	"encoding/json/v2"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"google.golang.org/genai"
)

func TestErrorStatusAndRetryInfo(t *testing.T) {
	for _, tc := range []struct {
		code       int
		typ        string
		wantCode   int
		wantStatus string
	}{
		{503, "", 503, "UNAVAILABLE"}, {529, "overloaded_error", 503, "UNAVAILABLE"},
		{504, "", 504, "DEADLINE_EXCEEDED"}, {408, "", 504, "DEADLINE_EXCEEDED"},
		{429, "throttlingException", 429, "RESOURCE_EXHAUSTED"},
		{400, "FAILED_PRECONDITION", 400, "FAILED_PRECONDITION"},
		{409, "ALREADY_EXISTS", 409, "ALREADY_EXISTS"},
		{499, "", 499, "CANCELLED"}, {501, "", 501, "UNIMPLEMENTED"},
		{502, "", 500, "INTERNAL"}, {500, "DATA_LOSS", 500, "DATA_LOSS"},
	} {
		t.Run(fmt.Sprint(tc.code)+tc.typ, func(t *testing.T) {
			err := &provider.ProviderError{Code: tc.code, Type: tc.typ, Message: "busy", RetryAfter: 2250 * time.Millisecond}
			rec := httptest.NewRecorder()
			writeError(rec, 500, err)
			var payload struct {
				Error genai.APIError `json:"error"`
			}
			if e := json.Unmarshal(rec.Body.Bytes(), &payload); e != nil {
				t.Fatal(e)
			}
			if rec.Code != tc.wantCode || payload.Error.Code != tc.wantCode || payload.Error.Status != tc.wantStatus || payload.Error.Message != "busy" || rec.Header().Get("Retry-After") != "3" {
				t.Fatalf("status=%d headers=%v error=%+v", rec.Code, rec.Header(), payload.Error)
			}
			if len(payload.Error.Details) != 1 || payload.Error.Details[0]["retryDelay"] != "2.25s" || payload.Error.Details[0]["@type"] != "type.googleapis.com/google.rpc.RetryInfo" {
				t.Fatalf("retry details=%v", payload.Error.Details)
			}
			acc := NewStreamingAccumulator("r1", "test", func(response GenerateContentResponse) error {
				if response.Error.Code != tc.wantCode || response.Error.Status != tc.wantStatus || response.Error.Details[0]["retryDelay"] != "2.25s" {
					t.Fatalf("stream error=%+v", response.Error)
				}
				return nil
			})
			if e := acc.Error(err); e != nil {
				t.Fatal(e)
			}
		})
	}
}
