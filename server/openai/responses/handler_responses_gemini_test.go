package responses

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	"github.com/adrianliechti/wingman/pkg/provider/google"

	"github.com/go-chi/chi/v5"
	"github.com/joho/godotenv"
	"google.golang.org/genai/interactions/models/interactions"
)

// Use the real HTTP handler, signature adapter, Gemini SDK, and provider in
// both modes. Only Google's transport is replaced in this regression test.
func TestResponsesGeminiSignedReplay(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var requests atomic.Int32
			client := &http.Client{Transport: boundaryTransport(func(r *http.Request) (*http.Response, error) {
				number := requests.Add(1)
				var request interactions.CreateModelInteraction
				if err := json.UnmarshalRead(r.Body, &request); err != nil {
					t.Fatal(err)
				}
				if r.URL.Path != "/v1beta/interactions" || request.Model != "gemini-4-argon" || request.Store == nil || *request.Store {
					t.Fatalf("unexpected Gemini request: %s %+v", r.URL.Path, request)
				}
				events := geminiReplayEvents()
				if number == 2 {
					steps := request.Input.ArrayOfStep
					var timeline []string
					for _, step := range steps {
						switch {
						case step.UserInputStep != nil:
							timeline = append(timeline, "user")
						case step.ThoughtStep != nil:
							timeline = append(timeline, *step.ThoughtStep.Signature)
						case step.FunctionCallStep != nil:
							timeline = append(timeline, step.FunctionCallStep.ID)
						case step.ModelOutputStep != nil:
							timeline = append(timeline, step.ModelOutputStep.Content[0].TextContent.Text)
						case step.FunctionResultStep != nil:
							timeline = append(timeline, "result:"+step.FunctionResultStep.CallID)
						}
					}
					want := []string{"user", "sig1", "c1", "sig2", "Checking history.", "c2", "sig3", "result:c1", "result:c2"}
					if !reflect.DeepEqual(timeline, want) {
						t.Errorf("Gemini replay timeline = %v, want %v", timeline, want)
						return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_request","message":"Request contains an invalid argument."}}`)), Request: r}, nil
					}
					if steps[2].FunctionCallStep.Name != "glob" || steps[2].FunctionCallStep.Arguments["pattern"] != "**/AGENTS.md" ||
						steps[5].FunctionCallStep.Name != "run_command" || steps[5].FunctionCallStep.Arguments["command"] != "git log --oneline -5" ||
						*steps[7].FunctionResultStep.Result.Str != "ok" || *steps[8].FunctionResultStep.Result.Str != "ok" {
						t.Error("tool names, arguments, or results changed on replay")
					}
					events = geminiSSE(
						`{"event_type":"interaction.created","interaction":{"id":"r2","model":"gemini-4-argon","status":"in_progress"}}`,
						`{"event_type":"step.start","index":0,"step":{"type":"model_output","content":[{"type":"text","text":"DONE"}]}}`,
						`{"event_type":"step.stop","index":0}`,
						`{"event_type":"interaction.completed","interaction":{"id":"r2","status":"completed"}}`,
					)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(events)), Request: r}, nil
			})}
			server := geminiResponsesServer(t, "test-token", client, "gemini-4-argon")
			input := []map[string]any{{"role": "user", "content": "Inspect the repository."}}
			tools := geminiReplayTools()
			output := postGeminiResponses(t, server.URL, "gemini-4-argon", input, tools, stream)
			input = append(input, output...)
			for _, id := range []string{"c1", "c2"} {
				input = append(input, map[string]any{"type": "function_call_output", "call_id": id, "output": "ok"})
			}
			output = postGeminiResponses(t, server.URL, "gemini-4-argon", input, tools, stream)
			if requests.Load() != 2 || len(output) != 1 {
				t.Fatalf("requests=%d, output=%v", requests.Load(), output)
			}
		})
	}
}

func TestResponsesGeminiTextBeforeThoughtSummary(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			client := &http.Client{Transport: boundaryTransport(func(r *http.Request) (*http.Response, error) {
				var request interactions.CreateModelInteraction
				if err := json.UnmarshalRead(r.Body, &request); err != nil {
					t.Fatal(err)
				}
				steps := request.Input.ArrayOfStep
				if len(steps) != 6 || steps[1].ThoughtStep == nil || steps[1].ThoughtStep.Signature == nil ||
					steps[2].ModelOutputStep.Content[0].TextContent.Text != "Checking history." ||
					steps[3].ThoughtStep.Summary[0].TextContent.Text != "Run the history command." ||
					steps[4].FunctionCallStep.ID != "c1" || steps[5].FunctionResultStep.CallID != "c1" {
					t.Error("thinking model turn did not start with a thought block")
					return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_request","message":"Model turns with thought summaries must start with a thought block in thinking models."}}`)), Request: r}, nil
				}
				events := geminiSSE(
					`{"event_type":"interaction.created","interaction":{"id":"r2","model":"gemini-4-argon","status":"in_progress"}}`,
					`{"event_type":"step.start","index":0,"step":{"type":"model_output","content":[{"type":"text","text":"DONE"}]}}`,
					`{"event_type":"step.stop","index":0}`,
					`{"event_type":"interaction.completed","interaction":{"id":"r2","status":"completed"}}`,
				)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(events)), Request: r}, nil
			})}
			server := geminiResponsesServer(t, "test-token", client, "gemini-4-argon")
			input := []map[string]any{
				{"role": "user", "content": "Inspect the repository."},
				{"role": "assistant", "content": "Checking history."},
				{"type": "reasoning", "summary": []map[string]any{{"type": "summary_text", "text": "Run the history command."}}},
				{"type": "function_call", "call_id": "c1", "name": "run_command", "arguments": `{"command":"git log --oneline -5"}`},
				{"type": "function_call_output", "call_id": "c1", "output": "success"},
			}
			postGeminiResponses(t, server.URL, "gemini-4-argon", input, geminiReplayTools(), stream)
		})
	}
}

func geminiSSE(events ...string) string {
	var result strings.Builder
	for _, event := range events {
		fmt.Fprintf(&result, "data: %s\n\n", event)
	}
	return result.String()
}

func geminiReplayEvents() string {
	return geminiSSE(
		`{"event_type":"interaction.created","interaction":{"id":"r1","model":"gemini-4-argon","status":"in_progress"}}`,
		`{"event_type":"step.start","index":0,"step":{"type":"thought"}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_signature","signature":"sig1"}}`,
		`{"event_type":"step.stop","index":0}`,
		`{"event_type":"step.start","index":1,"step":{"type":"function_call","id":"c1","name":"glob","arguments":{"pattern":"**/AGENTS.md"}}}`,
		`{"event_type":"step.stop","index":1}`,
		`{"event_type":"step.start","index":2,"step":{"type":"thought","signature":"sig2"}}`,
		`{"event_type":"step.stop","index":2}`,
		`{"event_type":"step.start","index":3,"step":{"type":"model_output","content":[{"type":"text","text":"Checking history."}]}}`,
		`{"event_type":"step.stop","index":3}`,
		`{"event_type":"step.start","index":4,"step":{"type":"function_call","id":"c2","name":"run_command","arguments":{"command":"git log --oneline -5"}}}`,
		`{"event_type":"step.stop","index":4}`,
		`{"event_type":"step.start","index":5,"step":{"type":"thought","signature":"sig3"}}`,
		`{"event_type":"step.stop","index":5}`,
		`{"event_type":"interaction.completed","interaction":{"id":"r1","status":"completed"}}`,
	)
}

func geminiReplayTools() []map[string]any {
	var tools []map[string]any
	for _, pair := range [][2]string{{"glob", "pattern"}, {"run_command", "command"}} {
		tools = append(tools, map[string]any{"type": "function", "name": pair[0], "parameters": map[string]any{
			"type": "object", "properties": map[string]any{pair[1]: map[string]any{"type": "string"}}, "required": []string{pair[1]},
		}})
	}
	return tools
}

func geminiResponsesServer(t *testing.T, key string, client *http.Client, model string) *httptest.Server {
	t.Helper()
	completer, err := google.NewCompleter(model, google.WithToken(key), google.WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(model, signatures.ScopedTo(model, completer))
	router := chi.NewRouter()
	router.Route("/v1", New(cfg).Attach)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server
}

func postGeminiResponses(t *testing.T, url, model string, input, tools []map[string]any, stream bool) []map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"model": model, "input": input, "tools": tools, "stream": stream, "store": false,
		"include": []string{"reasoning.encrypted_content"}, "reasoning": map[string]any{"effort": "low", "summary": "auto"}, "max_output_tokens": 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 90 * time.Second}
	response, err := client.Post(url+"/v1/responses", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("Responses HTTP %d: %s", response.StatusCode, data)
	}
	if stream {
		var completed jsontext.Value
		var doneItems []map[string]any
		var added []string
		for line := range strings.SplitSeq(string(data), "\n") {
			payload, ok := strings.CutPrefix(line, "data: ")
			if !ok {
				continue
			}
			var event struct {
				Type     string         `json:"type"`
				Response jsontext.Value `json:"response"`
				Index    int            `json:"output_index"`
				Item     map[string]any `json:"item"`
			}
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				t.Fatal(err)
			}
			if event.Type == "error" || event.Type == "response.failed" {
				t.Fatalf("Responses stream failed: %s", payload)
			}
			if event.Type == "response.completed" {
				completed = event.Response
			}
			if event.Type == "response.output_item.added" {
				if event.Index != len(added) {
					t.Fatalf("output index %d, want %d", event.Index, len(added))
				}
				added = append(added, event.Item["id"].(string))
			}
			if event.Type == "response.output_item.done" {
				if event.Index >= len(added) || added[event.Index] != event.Item["id"] || event.Index != len(doneItems) {
					t.Fatalf("item completed out of order: %s", payload)
				}
				doneItems = append(doneItems, event.Item)
			}
		}
		var snapshot struct {
			Output []map[string]any `json:"output"`
		}
		if err := json.Unmarshal(completed, &snapshot); err != nil || !reflect.DeepEqual(doneItems, snapshot.Output) {
			t.Fatalf("streamed items differ from completed snapshot (%v)", err)
		}
		data = completed
	}
	var result struct {
		Status string           `json:"status"`
		Output []map[string]any `json:"output"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.Status != "completed" {
		t.Fatalf("Responses did not complete: %s (%v)", data, err)
	}
	return result.Output
}

// Opt in to paid real-Argon tests through /v1/responses with client-side
// history replay: WINGMAN_GOOGLE_LIVE=1 go test -run TestResponsesGeminiLiveReplay.
func TestResponsesGeminiLiveReplay(t *testing.T) {
	if os.Getenv("WINGMAN_GOOGLE_LIVE") != "1" {
		t.Skip("set WINGMAN_GOOGLE_LIVE=1 to test Gemini through Responses")
	}
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		values, _ := godotenv.Read("../../../.env")
		key = values["GEMINI_API_KEY"]
	}
	if key == "" {
		t.Fatal("GEMINI_API_KEY is required")
	}
	model := os.Getenv("WINGMAN_GOOGLE_LIVE_MODELS")
	if model == "" {
		model = "gemini-4-argon"
	}
	server := geminiResponsesServer(t, key, &http.Client{Timeout: 90 * time.Second}, model)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			input := []map[string]any{{"role": "user", "content": "Inspect a Go repository using tools. First call glob twice for **/AGENTS.md and **/CLAUDE.md. Wait for their results. Then explain your next action and call glob for pkg/agent/*.go and run_command for git log --oneline -5. Wait for their results, then reply with DONE. Do not skip either stage."}}
			calls, thoughts := 0, 0
			for turn := range 5 {
				output := postGeminiResponses(t, server.URL, model, input, geminiReplayTools(), stream)
				input = append(input, output...)
				var results []map[string]any
				var timeline []string
				for _, item := range output {
					kind, _ := item["type"].(string)
					timeline = append(timeline, kind)
					if kind == "reasoning" && item["encrypted_content"] != nil && item["encrypted_content"] != "" {
						thoughts++
					}
					if kind == "function_call" {
						calls++
						text := "pkg/agent/agent.go\npkg/agent/tools.go\npkg/agent/agent_test.go"
						if item["name"] == "run_command" {
							text = "ce13eec Improve Protocols\nc0b7029 Improve Internet MCP & Tools"
						}
						results = append(results, map[string]any{"type": "function_call_output", "call_id": item["call_id"], "output": text})
					}
				}
				t.Logf("turn %d: %v, %d tool results", turn+1, timeline, len(results))
				if len(results) == 0 {
					encoded, _ := json.Marshal(output)
					if calls < 4 || thoughts == 0 || !bytes.Contains(encoded, []byte("DONE")) {
						t.Fatalf("incomplete tool workflow: %d calls, %d signatures, output %s", calls, thoughts, encoded)
					}
					return
				}
				input = append(input, results...)
			}
			t.Fatal("tool workflow did not finish")
		})
	}
}
