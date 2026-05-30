package errors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// neverCalledCompleter fails the test if Complete is ever invoked.
type neverCalledCompleter struct {
	t *testing.T
}

func (c *neverCalledCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		c.t.Helper()
		c.t.Fatal("completer should not be reached for invalid tool requests")
		yield(nil, errors.New("unreachable"))
	}
}

func postJSON(t *testing.T, url string, body any, headers map[string]string) (*http.Response, []byte) {
	t.Helper()

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp, raw
}

// TestOpenAIResponses_RejectsProprietaryTool sends an OpenAI Responses
// request with `{type: "web_search"}` and verifies the error body matches
// the structure OpenAI returns for an unknown tool type:
//
//	{"error":{"type":"invalid_request_error","code":"invalid_value",
//	          "param":"tools[0].type","message":"Invalid value: '...'..."}}
func TestOpenAIResponses_RejectsProprietaryTool(t *testing.T) {
	server := newWingmanServer(&neverCalledCompleter{t: t}, "test-model")
	defer server.Close()

	body := map[string]any{
		"model": "test-model",
		"input": "hi",
		"tools": []any{map[string]any{"type": "web_search"}},
	}

	resp, raw := postJSON(t, server.URL+"/v1/responses", body, nil)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Param   string `json:"param"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v; body: %s", err, string(raw))
	}

	if parsed.Error.Type != "invalid_request_error" {
		t.Errorf("error.type = %q, want invalid_request_error", parsed.Error.Type)
	}
	if parsed.Error.Code != "invalid_value" {
		t.Errorf("error.code = %q, want invalid_value", parsed.Error.Code)
	}
	if parsed.Error.Param != "tools[0].type" {
		t.Errorf("error.param = %q, want tools[0].type", parsed.Error.Param)
	}
	if !strings.Contains(parsed.Error.Message, "'web_search'") {
		t.Errorf("error.message = %q, expected to contain 'web_search'", parsed.Error.Message)
	}
	if !strings.Contains(parsed.Error.Message, "Supported values") {
		t.Errorf("error.message = %q, expected to mention supported values", parsed.Error.Message)
	}
}

// TestOpenAIChat_RejectsProprietaryTool verifies the chat completions
// surface also rejects unknown tool types in the OpenAI error shape.
// Chat only supports `function`; everything else fails.
func TestOpenAIChat_RejectsProprietaryTool(t *testing.T) {
	server := newChatServer(&neverCalledCompleter{t: t}, "test-model")
	defer server.Close()

	body := map[string]any{
		"model":    "test-model",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"tools":    []any{map[string]any{"type": "file_search"}},
	}

	resp, raw := postJSON(t, server.URL+"/v1/chat/completions", body, nil)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Param   string `json:"param"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v; body: %s", err, string(raw))
	}

	if parsed.Error.Type != "invalid_request_error" {
		t.Errorf("error.type = %q, want invalid_request_error", parsed.Error.Type)
	}
	if parsed.Error.Code != "invalid_value" {
		t.Errorf("error.code = %q, want invalid_value", parsed.Error.Code)
	}
	if parsed.Error.Param != "tools[0].type" {
		t.Errorf("error.param = %q, want tools[0].type", parsed.Error.Param)
	}
	if !strings.Contains(parsed.Error.Message, "'file_search'") {
		t.Errorf("error.message = %q, expected to contain 'file_search'", parsed.Error.Message)
	}
}

// TestOpenAIResponses_RejectsInvalidRole verifies wingman rejects an
// unknown role in the OpenAI Responses input items with the OpenAI error
// shape (code: invalid_value, param: input[<i>].role).
func TestOpenAIResponses_RejectsInvalidRole(t *testing.T) {
	server := newWingmanServer(&neverCalledCompleter{t: t}, "test-model")
	defer server.Close()

	body := map[string]any{
		"model": "test-model",
		"input": []map[string]any{{
			"type":    "message",
			"role":    "wat",
			"content": []map[string]any{{"type": "input_text", "text": "hi"}},
		}},
	}

	resp, raw := postJSON(t, server.URL+"/v1/responses", body, nil)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Param   string `json:"param"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v; body: %s", err, string(raw))
	}

	if parsed.Error.Code != "invalid_value" {
		t.Errorf("code = %q, want invalid_value", parsed.Error.Code)
	}
	if parsed.Error.Param != "input[0].role" {
		t.Errorf("param = %q, want input[0].role", parsed.Error.Param)
	}
	if !strings.Contains(parsed.Error.Message, "'wat'") {
		t.Errorf("message = %q, expected to contain 'wat'", parsed.Error.Message)
	}
}

// TestOpenAIChat_RejectsInvalidRole verifies the chat completions surface
// rejects unknown roles with the OpenAI error shape.
func TestOpenAIChat_RejectsInvalidRole(t *testing.T) {
	server := newChatServer(&neverCalledCompleter{t: t}, "test-model")
	defer server.Close()

	body := map[string]any{
		"model":    "test-model",
		"messages": []map[string]any{{"role": "wat", "content": "hi"}},
	}

	resp, raw := postJSON(t, server.URL+"/v1/chat/completions", body, nil)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Param   string `json:"param"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v; body: %s", err, string(raw))
	}

	if parsed.Error.Code != "invalid_value" {
		t.Errorf("code = %q, want invalid_value", parsed.Error.Code)
	}
	if parsed.Error.Param != "messages[0].role" {
		t.Errorf("param = %q, want messages[0].role", parsed.Error.Param)
	}
	if !strings.Contains(parsed.Error.Message, "'wat'") {
		t.Errorf("message = %q, expected to contain 'wat'", parsed.Error.Message)
	}
}

// TestAnthropicMessages_RejectsInvalidRole verifies wingman rejects an
// unknown role with Anthropic's error shape.
func TestAnthropicMessages_RejectsInvalidRole(t *testing.T) {
	server := newAnthropicServer(&neverCalledCompleter{t: t}, "test-model")
	defer server.Close()

	body := map[string]any{
		"model":      "test-model",
		"max_tokens": 256,
		"messages":   []map[string]any{{"role": "wat", "content": "hi"}},
	}

	resp, raw := postJSON(t, server.URL+"/v1/messages", body, map[string]string{
		"x-api-key":         "test",
		"anthropic-version": "2023-06-01",
	})

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v; body: %s", err, string(raw))
	}

	if parsed.Error.Type != "invalid_request_error" {
		t.Errorf("error.type = %q, want invalid_request_error", parsed.Error.Type)
	}
	if !strings.Contains(parsed.Error.Message, "messages.0") {
		t.Errorf("message = %q, expected to contain 'messages.0'", parsed.Error.Message)
	}
	if !strings.Contains(parsed.Error.Message, "wat") {
		t.Errorf("message = %q, expected to contain 'wat'", parsed.Error.Message)
	}
}

// TestAnthropicMessages_RejectsProprietaryTool sends an Anthropic Messages
// request with `{type: "web_search_20250305"}` and verifies the error body
// matches the structure Anthropic returns for an unknown tool tag:
//
//	{"type":"error","error":{"type":"invalid_request_error",
//	                          "message":"tools.0: Input tag '...' ..."}}
func TestAnthropicMessages_RejectsProprietaryTool(t *testing.T) {
	server := newAnthropicServer(&neverCalledCompleter{t: t}, "test-model")
	defer server.Close()

	body := map[string]any{
		"model":      "test-model",
		"max_tokens": 256,
		"messages":   []map[string]any{{"role": "user", "content": "hi"}},
		"tools": []any{
			map[string]any{"type": "web_search_20250305", "name": "web_search"},
		},
	}

	resp, raw := postJSON(t, server.URL+"/v1/messages", body, map[string]string{
		"x-api-key":         "test",
		"anthropic-version": "2023-06-01",
	})

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v; body: %s", err, string(raw))
	}

	if parsed.Type != "error" {
		t.Errorf("type = %q, want error", parsed.Type)
	}
	if parsed.Error.Type != "invalid_request_error" {
		t.Errorf("error.type = %q, want invalid_request_error", parsed.Error.Type)
	}
	if !strings.Contains(parsed.Error.Message, "tools.0") {
		t.Errorf("error.message = %q, expected to contain 'tools.0'", parsed.Error.Message)
	}
	if !strings.Contains(parsed.Error.Message, "web_search_20250305") {
		t.Errorf("error.message = %q, expected to contain 'web_search_20250305'", parsed.Error.Message)
	}
}

