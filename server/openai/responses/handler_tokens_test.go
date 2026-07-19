package responses

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
)

func postInputTokens(t *testing.T, body string) InputTokensResponse {
	t.Helper()

	cfg := &config.Config{Policy: noop.New()}
	h := New(cfg)

	req := httptest.NewRequest(http.MethodPost, "/responses/input_tokens", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.handleInputTokens(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp InputTokensResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Object != "response.input_tokens" {
		t.Fatalf("object = %q, want response.input_tokens", resp.Object)
	}
	return resp
}

func TestInputTokensString(t *testing.T) {
	// Upstream measured: {"model":"gpt-5","input":"hello world"} → 8.
	resp := postInputTokens(t, `{"model":"gpt-5","input":"hello world"}`)
	if resp.InputTokens < 5 || resp.InputTokens > 12 {
		t.Errorf("input_tokens = %d, want ≈8 (upstream measurement)", resp.InputTokens)
	}
}

func TestInputTokensMessages(t *testing.T) {
	// Upstream measured: user "hello world" + assistant "hi" → 15.
	resp := postInputTokens(t, `{
		"model": "gpt-5",
		"input": [
			{"role": "user", "content": "hello world"},
			{"role": "assistant", "content": "hi"}
		]
	}`)
	if resp.InputTokens < 10 || resp.InputTokens > 20 {
		t.Errorf("input_tokens = %d, want ≈15 (upstream measurement)", resp.InputTokens)
	}
}

func TestInputTokensInstructionsAndTools(t *testing.T) {
	resp := postInputTokens(t, `{
		"model": "gpt-4o",
		"instructions": "You are terse.",
		"input": "Summarize the weekly report in three bullet points.",
		"tools": [{
			"type": "function",
			"name": "get_report",
			"description": "Fetch the weekly report",
			"parameters": {"type": "object", "properties": {"week": {"type": "string"}}}
		}]
	}`)
	if resp.InputTokens < 30 || resp.InputTokens > 90 {
		t.Errorf("input_tokens = %d, want a plausible mid-double-digit estimate", resp.InputTokens)
	}
}

func TestInputTokensBadBody(t *testing.T) {
	cfg := &config.Config{Policy: noop.New()}
	h := New(cfg)

	req := httptest.NewRequest(http.MethodPost, "/responses/input_tokens", bytes.NewBufferString("{"))
	rec := httptest.NewRecorder()
	h.handleInputTokens(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
