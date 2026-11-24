package server_test

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// Paid opt-in test. An upstream rejection must reach every client as that
// API's own invalid-request error and status, whichever vendor rejected it
// and whether the rejection arrived as an HTTP status or inside a stream.
// The SDKs choose their exception class (BadRequestError, ClientError) from it.
//
//	WINGMAN_PROVIDER_LIVE=1 go test ./server -run TestUpstreamErrorKindLive -v -count=1
func TestUpstreamErrorKindLive(t *testing.T) {
	invalidName := map[string]any{"name": "bad name", "schema": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}}}
	// OpenAI reports an invalid schema as an error event after HTTP 200.
	invalidSchema := map[string]any{"name": "lookup", "schema": map[string]any{"type": "STRING"}}
	triggers := []struct {
		name     string
		tool     map[string]any
		backends []string
	}{
		{"tool name", invalidName, []string{"openai", "anthropic", "gemini"}},
		{"tool schema", invalidSchema, []string{"openai", "anthropic"}},
	}
	type surface struct {
		name    string
		request func(base, model string, stream bool, tool map[string]any) (string, http.Header, map[string]any)
		// rejected reports whether the body is this API's invalid-request error.
		rejected func(body map[string]any) bool
		openai   bool
	}
	openaiError := func(body map[string]any) bool {
		e, _ := body["error"].(map[string]any)
		return e["type"] == "invalid_request_error"
	}
	surfaces := []surface{
		{"responses", func(base, model string, stream bool, tool map[string]any) (string, http.Header, map[string]any) {
			return base + "/v1/responses", http.Header{"Authorization": {"Bearer " + replayKey}}, map[string]any{
				"model": model, "stream": stream, "input": "hi",
				"tools": []any{map[string]any{"type": "function", "name": tool["name"], "parameters": tool["schema"]}},
			}
		}, openaiError, true},
		{"chat", func(base, model string, stream bool, tool map[string]any) (string, http.Header, map[string]any) {
			return base + "/v1/chat/completions", http.Header{"Authorization": {"Bearer " + replayKey}}, map[string]any{
				"model": model, "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "hi"}},
				"tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": tool["name"], "parameters": tool["schema"]}}},
			}
		}, openaiError, true},
		{"messages", func(base, model string, stream bool, tool map[string]any) (string, http.Header, map[string]any) {
			return base + "/v1/messages", http.Header{"x-api-key": {replayKey}, "anthropic-version": {"2023-06-01"}}, map[string]any{
				"model": model, "stream": stream, "max_tokens": 64, "messages": []any{map[string]any{"role": "user", "content": "hi"}},
				"tools": []any{map[string]any{"name": tool["name"], "input_schema": tool["schema"]}},
			}
		}, func(body map[string]any) bool {
			e, _ := body["error"].(map[string]any)
			return body["type"] == "error" && e["type"] == "invalid_request_error"
		}, false},
		{"gemini", func(base, model string, stream bool, tool map[string]any) (string, http.Header, map[string]any) {
			action := "generateContent"
			if stream {
				action = "streamGenerateContent?alt=sse"
			}
			return base + "/v1beta/models/" + model + ":" + action, http.Header{"x-goog-api-key": {replayKey}}, map[string]any{
				"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}}},
				"tools":    []any{map[string]any{"functionDeclarations": []any{map[string]any{"name": tool["name"], "parametersJsonSchema": tool["schema"]}}}},
			}
		}, func(body map[string]any) bool {
			e, _ := body["error"].(map[string]any)
			return e["status"] == "INVALID_ARGUMENT" && e["code"] == float64(http.StatusBadRequest)
		}, false},
	}

	backends := map[string]liveBackend{}
	for _, backend := range liveBackends(t) {
		backends[backend.name] = backend
	}
	for _, trigger := range triggers {
		for _, name := range trigger.backends {
			backend := backends[name]
			t.Run(trigger.name+"/"+name, func(t *testing.T) {
				t.Parallel()
				base := liveServer(t, backend, http.DefaultTransport)
				for _, surface := range surfaces {
					for _, stream := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/stream=%t", surface.name, stream), func(t *testing.T) {
							url, header, body := surface.request(base, name, stream, trigger.tool)
							status, raw := postStatus(t, url, header, body)
							var decoded map[string]any
							if status != http.StatusBadRequest || json.Unmarshal(raw, &decoded) != nil || !surface.rejected(decoded) {
								t.Fatalf("HTTP %d: %s", status, raw)
							}
							// OpenAI clients act on error.code; another vendor's code would mislead them.
							if e, _ := decoded["error"].(map[string]any); surface.openai && name != "openai" && e["code"] != nil {
								t.Fatalf("%s error code reached an OpenAI client: %s", name, raw)
							}
						})
					}
				}
			})
		}
	}
}

func postStatus(t *testing.T, url string, header http.Header, body map[string]any) (int, []byte) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header = header.Clone()
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, raw
}
