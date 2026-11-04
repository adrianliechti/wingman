package shared

import (
	"encoding/json"
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
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if string(body.Error) != raw {
			t.Fatalf("error fields changed: got %s, want %s", body.Error, raw)
		}
	}
}
