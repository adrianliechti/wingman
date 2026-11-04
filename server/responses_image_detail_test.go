package server_test

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/server"
)

func TestResponsesPreservesImageDetail(t *testing.T) {
	const imageURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="
	for _, source := range []string{"message", "function", "custom"} {
		for _, stream := range []bool{false, true} {
			for _, tc := range []struct {
				name, detail, want string
				valid              bool
			}{
				{"omitted", "", "", true}, {"null", "null", "", true},
				{"auto", `"auto"`, "auto", true}, {"low", `"low"`, "low", true},
				{"high", `"high"`, "high", true}, {"original", `"original"`, "original", true},
				{"empty", `""`, "", false}, {"unknown", `"full"`, "", false},
				{"wrong type", `123`, "", false},
			} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", source, stream, tc.name), func(t *testing.T) {
					calls := 0
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						if r.URL.Path != "/responses" {
							t.Errorf("unexpected upstream path: %s", r.URL.Path)
						}
						var body struct {
							Input []struct {
								Type    string                      `json:"type"`
								Content []map[string]jsontext.Value `json:"content"`
								Output  []map[string]jsontext.Value `json:"output"`
							} `json:"input"`
						}
						if err := json.UnmarshalRead(r.Body, &body); err != nil {
							t.Error(err)
							return
						}
						var image map[string]jsontext.Value
						for _, item := range body.Input {
							parts := item.Content
							if source != "message" {
								parts = item.Output
							}
							for _, part := range parts {
								if string(part["type"]) == `"input_image"` {
									image = part
								}
							}
						}
						if image == nil {
							t.Errorf("upstream image missing: %+v", body)
							return
						}
						got := image["detail"]
						if tc.want == "" {
							if len(got) > 0 {
								t.Errorf("default detail must be omitted: %s", got)
							}
						} else if string(got) != `"`+tc.want+`"` {
							t.Errorf("detail = %s, want %s", got, tc.want)
						}
						if string(image["image_url"]) != `"`+imageURL+`"` {
							t.Errorf("image changed: %s", image["image_url"])
						}
						w.Header().Set("Content-Type", "text/event-stream")
						io.WriteString(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"model\":\"gpt-6-luna\",\"status\":\"in_progress\",\"output\":[]}}\n\n")
						io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"model\":\"gpt-6-luna\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0,\"total_tokens\":1}}}\n\n")
					}))
					defer upstream.Close()
					responder, err := openai.NewResponder(upstream.URL, "gpt-6-luna", openai.WithMaxRetries(0))
					if err != nil {
						t.Fatal(err)
					}
					cfg := &config.Config{Policy: noop.New()}
					cfg.RegisterCompleter("vision", responder)
					s, err := server.New(cfg)
					if err != nil {
						t.Fatal(err)
					}
					image := map[string]any{"type": "input_image", "image_url": imageURL}
					if tc.detail != "" {
						image["detail"] = jsontext.Value(tc.detail)
					}
					var input []any
					switch source {
					case "message":
						input = []any{map[string]any{"role": "user", "content": []any{image}}}
					case "function":
						input = []any{map[string]any{"type": "function_call", "call_id": "f", "name": "lookup", "arguments": "{}"}, map[string]any{"type": "function_call_output", "call_id": "f", "output": []any{image}}}
					case "custom":
						input = []any{map[string]any{"type": "custom_tool_call", "call_id": "c", "name": "lookup", "input": "query"}, map[string]any{"type": "custom_tool_call_output", "call_id": "c", "output": []any{image}}}
					}
					payload, err := json.Marshal(map[string]any{"model": "vision", "stream": stream, "input": input})
					if err != nil {
						t.Fatal(err)
					}
					w := httptest.NewRecorder()
					s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(payload)))
					want := http.StatusOK
					if !tc.valid {
						want = http.StatusBadRequest
					}
					if w.Code != want {
						t.Fatalf("status %d, want %d: %s", w.Code, want, w.Body.String())
					}
					if (calls == 1) != tc.valid {
						t.Fatalf("upstream calls = %d, valid=%t", calls, tc.valid)
					}
				})
			}
		}
	}
}
