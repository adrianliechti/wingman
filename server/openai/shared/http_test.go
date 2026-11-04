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
			Code: http.StatusBadRequest, Type: upstream.Type, Message: upstream.Message,
			Err: &upstream, RetryAfter: 2 * time.Second,
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
