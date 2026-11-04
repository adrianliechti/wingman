package protocol_test

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
)

func TestChatStreamIdentitySurvivesSparseMetadata(t *testing.T) {
	for _, lateID := range []bool{false, true} {
		t.Run(fmt.Sprintf("late_id=%v", lateID), func(t *testing.T) {
			local := gateway(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				id := `"id":"chatcmpl-sparse",`
				if lateID {
					id = ""
				}
				fmt.Fprintf(w, "data: {%s\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\n", id)
				if lateID {
					id = `"id":"chatcmpl-late",`
				} else {
					id = ""
				}
				fmt.Fprintf(w, "data: {%s\"choices\":[{\"index\":0,\"delta\":{\"content\":\" world\"}}]}\n\n", id)
				io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\ndata: [DONE]\n\n")
			})
			p := protocols(true)[0]
			status, _, raw := post(t, local, p.path, p.body)
			requireStatus(t, status, 200, raw)
			events := frames(t, raw)
			id, ok := events[0].Data["id"].(string)
			if !ok || id == "" {
				t.Fatal("missing first chunk ID")
			}
			if !lateID {
				equal(t, "upstream ID", id, "chatcmpl-sparse")
			}
			for _, e := range events {
				if e.Raw == "[DONE]" {
					continue
				}
				equal(t, "stable ID", e.Data["id"], id)
				equal(t, "stable timestamp", e.Data["created"], events[0].Data["created"])
				equal(t, "stable model", e.Data["model"], events[0].Data["model"])
			}
		})
	}
}

func TestLargeFragmentedToolArguments(t *testing.T) {
	arguments := `{"integer":9007199254740993,"quoted":"a\\\"b","emoji":"\uD83D\uDE80","text":"` + strings.Repeat("large-payload-", 8000) + `"}`
	want := exactObject(t, []byte(arguments))
	for _, stream := range []bool{false, true} {
		for _, p := range protocols(stream) {
			t.Run(fmt.Sprintf("%s/stream=%v", p.name, stream), func(t *testing.T) {
				local := gateway(t, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					for offset := 0; offset < len(arguments); {
						// Single bytes split numbers and JSON escapes; larger later
						// fragments keep this stress case inexpensive to run.
						size := 4093
						if offset < 100 {
							size = 1
						}
						end := min(offset+size, len(arguments))
						call := map[string]any{"index": 0, "function": map[string]any{"arguments": arguments[offset:end]}}
						if offset == 0 {
							call["id"] = "call_large"
							call["type"] = "function"
							call["function"].(map[string]any)["name"] = "record"
						}
						writeChunk(w, map[string]any{"tool_calls": []any{call}}, nil, nil)
						offset = end
					}
					writeChunk(w, map[string]any{}, "tool_calls", nil)
					io.WriteString(w, "data: [DONE]\n\n")
				})
				status, _, raw := post(t, local, p.path, p.body)
				requireStatus(t, status, 200, raw)
				got := toolArguments(t, p.name, stream, raw)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("tool arguments changed: integer=%v emoji=%v text length=%d", got["integer"], got["emoji"], len(fmt.Sprint(got["text"])))
				}
			})
		}
	}
}

func exactObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := jsonutil.UnmarshalRead(strings.NewReader(string(raw)), &value); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if value == nil {
		t.Fatal("missing JSON object")
	}
	return value
}

func toolArguments(t *testing.T, protocol string, stream bool, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	var fragments strings.Builder
	if stream {
		for _, e := range frames(t, raw) {
			if e.Raw == "[DONE]" {
				continue
			}
			data := exactObject(t, []byte(e.Raw))
			switch protocol {
			case "chat":
				if fragment, ok := at(data, "choices", 0, "delta", "tool_calls", 0, "function", "arguments").(string); ok {
					fragments.WriteString(fragment)
				}
			case "anthropic":
				if fragment, ok := at(data, "delta", "partial_json").(string); ok {
					fragments.WriteString(fragment)
				}
			case "responses":
				if e.Event == "response.completed" {
					body, _ = data["response"].(map[string]any)
				}
			case "gemini":
				if at(data, "candidates", 0, "finishReason") != nil {
					body = data
				}
			}
		}
		if protocol == "chat" || protocol == "anthropic" {
			return exactObject(t, []byte(fragments.String()))
		}
	} else {
		body = exactObject(t, raw)
	}
	var value any
	switch protocol {
	case "chat":
		value = at(body, "choices", 0, "message", "tool_calls", 0, "function", "arguments")
	case "responses":
		value = at(body, "output", 0, "arguments")
	case "anthropic":
		value = at(body, "content", 0, "input")
	case "gemini":
		value = at(body, "candidates", 0, "content", "parts", 0, "functionCall", "args")
	}
	if encoded, ok := value.(string); ok {
		return exactObject(t, []byte(encoded))
	}
	args, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("missing tool arguments (%s)", protocol)
	}
	return args
}

func TestConcurrentStreamsKeepRequestsIsolated(t *testing.T) {
	local := gateway(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
			t.Error(err)
			return
		}
		marker, _ := at(body, "messages", 0, "content").(string)
		if marker == "" {
			marker, _ = at(body, "messages", 0, "content", 0, "text").(string)
		}
		if !strings.HasPrefix(marker, "request-") {
			t.Errorf("missing request marker: %v", body)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for i := range 12 {
			writeChunk(w, map[string]any{"content": fmt.Sprintf("%s:%d|", marker, i)}, nil, nil)
		}
		writeChunk(w, map[string]any{}, "stop", nil)
		io.WriteString(w, "data: [DONE]\n\n")
	})
	for i := range 16 {
		p := protocols(true)[i%4]
		t.Run(fmt.Sprintf("%s/%d", p.name, i), func(t *testing.T) {
			t.Parallel()
			marker := fmt.Sprintf("request-%d", i)
			switch p.name {
			case "chat", "anthropic":
				p.body["messages"] = []any{map[string]any{"role": "user", "content": marker}}
			case "responses":
				p.body["input"] = marker
			case "gemini":
				p.body["contents"] = []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": marker}}}}
			}
			status, _, raw := post(t, local, p.path, p.body)
			requireStatus(t, status, 200, raw)
			var text, want strings.Builder
			for _, e := range frames(t, raw) {
				var value any
				switch p.name {
				case "chat":
					value = at(e.Data, "choices", 0, "delta", "content")
				case "responses":
					if e.Event == "response.output_text.delta" {
						value = e.Data["delta"]
					}
				case "anthropic":
					value = at(e.Data, "delta", "text")
				case "gemini":
					value = at(e.Data, "candidates", 0, "content", "parts", 0, "text")
				}
				if delta, ok := value.(string); ok {
					text.WriteString(delta)
				}
			}
			for j := range 12 {
				fmt.Fprintf(&want, "%s:%d|", marker, j)
			}
			equal(t, "isolated output", text.String(), want.String())
		})
	}
}

func TestEmbeddingBatchPreservesInputOrder(t *testing.T) {
	local := modalityGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"index":1,"embedding":[20]},{"index":0,"embedding":[10]}],"usage":{"prompt_tokens":2,"total_tokens":2}}`)
	})
	status, _, raw := post(t, local, "/v1/embeddings", map[string]any{"model": "embedding", "input": []string{"first", "second"}})
	requireStatus(t, status, 200, raw)
	body := object(t, raw)
	for i, want := range []float64{10, 20} {
		equal(t, "embedding index", at(body, "data", i, "index"), float64(i))
		equal(t, "embedding value", at(body, "data", i, "embedding", 0), want)
	}
}

func TestMalformedEmbeddingIndicesReturnProtocolError(t *testing.T) {
	for name, data := range map[string]string{
		"missing":      `[{"index":0,"embedding":[1]}]`,
		"duplicate":    `[{"index":0,"embedding":[1]},{"index":0,"embedding":[2]}]`,
		"negative":     `[{"index":-1,"embedding":[1]},{"index":1,"embedding":[2]}]`,
		"out_of_range": `[{"index":0,"embedding":[1]},{"index":2,"embedding":[2]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			local := modalityGateway(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"data":%s}`, data)
			})
			status, headers, raw := post(t, local, "/v1/embeddings", map[string]any{"model": "embedding", "input": []string{"first", "second"}})
			requireStatus(t, status, 502, raw)
			requireType(t, headers, "application/json")
			if at(object(t, raw), "error", "message") == nil {
				t.Fatal("missing error message")
			}
		})
	}
}

func TestErrorAfterFinishReasonIsNotSuccess(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, p := range protocols(stream) {
			t.Run(fmt.Sprintf("%s/stream=%v", p.name, stream), func(t *testing.T) {
				local := gateway(t, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					writeChunk(w, map[string]any{"content": "partial"}, "stop", nil)
					io.WriteString(w, "data: {\"error\":{\"message\":\"late upstream failure\",\"type\":\"server_error\",\"code\":\"server_error\"}}\n\n")
				})
				status, _, raw := post(t, local, p.path, p.body)
				if !stream {
					want := http.StatusBadGateway
					if p.name == "gemini" {
						want = http.StatusInternalServerError
					}
					requireStatus(t, status, want, raw)
					return
				}
				requireStatus(t, status, 200, raw)
				errors := 0
				for _, e := range frames(t, raw) {
					if e.Event == "response.completed" || e.Event == "message_stop" || e.Raw == "[DONE]" || at(e.Data, "choices", 0, "finish_reason") == "stop" || at(e.Data, "candidates", 0, "finishReason") == "STOP" {
						t.Fatalf("late error produced success: %s", raw)
					}
					if e.Data["error"] != nil || e.Event == "error" || e.Event == "response.failed" {
						errors++
					}
				}
				if errors == 0 {
					t.Fatal("lost late upstream error")
				}
			})
		}
	}
}

func TestEmptyImageUpstreamIsProtocolError(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(fmt.Sprintf("edit=%v", edit), func(t *testing.T) {
			local := modalityGateway(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"created":1,"data":[]}`)
			})
			var status int
			var headers http.Header
			var raw []byte
			if edit {
				ct, body := multipartBody(t, map[string]string{"model": "image", "prompt": "kite"}, "image", "kite.png", []byte("\x89PNG\r\n\x1a\ncontract"))
				status, headers, raw = request(t, local, http.MethodPost, "/v1/images/edits", ct, body)
			} else {
				status, headers, raw = post(t, local, "/v1/images/generations", map[string]any{"model": "image", "prompt": "kite"})
			}
			requireStatus(t, status, 502, raw)
			requireType(t, headers, "application/json")
			if at(object(t, raw), "error", "message") == nil {
				t.Fatal("missing error message")
			}
		})
	}
}
