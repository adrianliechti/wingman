package responses

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

type boundaryTransport func(*http.Request) (*http.Response, error)

func (f boundaryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Exercise the real Responses provider as well as the server's JSON and SSE
// paths. Missing/null phases must not merge items or inherit another phase.
func TestResponsesMessageBoundaries(t *testing.T) {
	for _, phases := range [][]string{
		{"commentary", "final_answer"},
		{"commentary", ""},
		{"", "final_answer"},
		{"", ""},
		{"commentary", "null"},
		{"null", "final_answer"},
		{"commentary", "commentary", "final_answer"},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/stream=%t", phases, stream), func(t *testing.T) {
				client := &http.Client{Transport: boundaryTransport(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(boundaryEvents(phases))), Request: r}, nil
				})}
				completer, err := openai.NewResponder("http://upstream.test", "gpt-test", openai.WithToken("test"), openai.WithClient(client))
				if err != nil {
					t.Fatal(err)
				}
				cfg := &config.Config{Policy: noop.New()}
				cfg.RegisterCompleter("test", completer)
				rec := httptest.NewRecorder()
				body := fmt.Sprintf(`{"model":"test","input":"check","stream":%t}`, stream)
				New(cfg).handleResponses(rec, httptest.NewRequest("POST", "/responses", strings.NewReader(body)))
				if rec.Code != 200 {
					t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
				}

				var result struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Output []struct {
						ID      string                  `json:"id"`
						Phase   string                  `json:"phase"`
						Content []struct{ Text string } `json:"content"`
					} `json:"output"`
				}
				added, done := map[string]string{}, map[string]string{}
				payload := rec.Body.Bytes()
				if stream {
					payload = nil
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data: ") {
							continue
						}
						var event struct {
							Type     string                     `json:"type"`
							Response json.RawMessage            `json:"response"`
							Item     struct{ ID, Phase string } `json:"item"`
						}
						if err := json.Unmarshal([]byte(line[6:]), &event); err != nil {
							t.Fatal(err)
						}
						switch event.Type {
						case "response.created", "response.in_progress":
							var response struct{ ID string }
							if err := json.Unmarshal(event.Response, &response); err != nil || response.ID != "resp_test" {
								t.Fatalf("lost native response ID in %s: %s (%v)", event.Type, event.Response, err)
							}
						case "response.completed":
							payload = event.Response
						case "response.output_item.added":
							if _, exists := added[event.Item.ID]; exists {
								t.Fatal("reused message id")
							}
							added[event.Item.ID] = event.Item.Phase
						case "response.output_item.done":
							done[event.Item.ID] = event.Item.Phase
						}
					}
				}
				if err := json.Unmarshal(payload, &result); err != nil {
					t.Fatalf("missing final response: %v", err)
				}
				if result.ID != "resp_test" || result.Status != "completed" || len(result.Output) != len(phases) {
					t.Fatalf("message boundaries lost: %+v", result)
				}
				for i, item := range result.Output {
					if item.ID != fmt.Sprintf("msg_%d", i) {
						t.Errorf("item %d replaced native ID: %q", i, item.ID)
					}
					want := phases[i]
					if want == "null" {
						want = ""
					}
					if item.Phase != want || len(item.Content) != 1 || item.Content[0].Text != fmt.Sprintf("message %d", i) {
						t.Errorf("item %d: %+v, want phase=%q and its original text", i, item, want)
					}
					if stream {
						start, hasStart := added[item.ID]
						end, hasEnd := done[item.ID]
						if !hasStart || !hasEnd || start != want || end != want {
							t.Errorf("item %d changed phase across its lifecycle: added=%q/%t done=%q/%t", i, start, hasStart, end, hasEnd)
						}
					}
				}
			})
		}
	}
}

func boundaryEvents(phases []string) string {
	var stream strings.Builder
	sequence := 0
	emit := func(kind string, event map[string]any) {
		event["type"], event["sequence_number"] = kind, sequence
		sequence++
		data, _ := json.Marshal(event)
		fmt.Fprintf(&stream, "event: %s\ndata: %s\n\n", kind, data)
	}
	emit("response.created", map[string]any{"response": map[string]any{"id": "resp_test", "model": "gpt-test", "status": "in_progress", "output": []any{}}})
	var output []any
	for i, phase := range phases {
		id := fmt.Sprintf("msg_%d", i)
		item := map[string]any{"id": id, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}
		if phase == "null" {
			item["phase"] = nil
		} else if phase != "" {
			item["phase"] = phase
		}
		emit("response.output_item.added", map[string]any{"output_index": i, "item": item})
		part := map[string]any{"type": "output_text", "text": "", "annotations": []any{}}
		emit("response.content_part.added", map[string]any{"output_index": i, "content_index": 0, "item_id": id, "part": part})
		text := fmt.Sprintf("message %d", i)
		emit("response.output_text.delta", map[string]any{"output_index": i, "content_index": 0, "item_id": id, "delta": text})
		emit("response.output_text.done", map[string]any{"output_index": i, "content_index": 0, "item_id": id, "text": text})
		part["text"] = text
		emit("response.content_part.done", map[string]any{"output_index": i, "content_index": 0, "item_id": id, "part": part})
		item["status"], item["content"] = "completed", []any{part}
		emit("response.output_item.done", map[string]any{"output_index": i, "item": item})
		output = append(output, item)
	}
	emit("response.completed", map[string]any{"response": map[string]any{"id": "resp_test", "model": "gpt-test", "status": "completed", "output": output}})
	return stream.String()
}
