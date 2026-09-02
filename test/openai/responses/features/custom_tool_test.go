package features_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

// Freeform custom tools are native only on OpenAI Responses. Every other
// backend emulates them as a function tool with a single string parameter
// (pkg/provider/tools/custom), so these tests cover both paths across the
// three emulating backends and the native one:
//
//	TEST_CUSTOM_TOOL_MODELS=bedrock-sonnet-4-6,claude-sonnet-4-6,gpt-5.4,gemini-3.8-flash \
//	  go test ./test/openai/responses/features -run TestCustomTool -v
//
// The payload deliberately carries the escape-heavy content that breaks
// JSON-argument tools: a Windows path, a raw-string regex, an em dash and
// nested quotes. On a freeform tool none of it is ever JSON-escaped.
func customToolModels() []string {
	if v := os.Getenv("TEST_CUSTOM_TOOL_MODELS"); v != "" {
		var names []string

		for s := range strings.SplitSeq(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				names = append(names, s)
			}
		}

		return names
	}

	return []string{"bedrock-sonnet-4-6", "claude-sonnet-4-6", "gpt-5.4", "gemini-3.8-flash"}
}

const runPythonDescription = "Run a Python script. This is a FREEFORM tool: send the raw source code, " +
	"do not wrap it in JSON."

func runPythonTool() map[string]any {
	return map[string]any{
		"type":        "custom",
		"name":        "run_python",
		"description": runPythonDescription,
	}
}

const escapeHeavyPrompt = `Use run_python once to print a dict containing:
- a Windows path C:\temp\x.txt
- a raw-string regex r"\d{4}"
- the quoted phrase "it's — fine"
Call the tool exactly once and write nothing else.`

// customToolCallInput returns the input of the single custom_tool_call output
// item, failing when the model produced a function_call instead — that is the
// signal the tool was declared as JSON rather than freeform.
func customToolCallInput(t *testing.T, label string, body map[string]any) string {
	t.Helper()

	var kinds []string

	for _, item := range body["output"].([]any) {
		obj, _ := item.(map[string]any)
		kinds = append(kinds, obj["type"].(string))

		if obj["type"] == "custom_tool_call" {
			if name, _ := obj["name"].(string); name != "run_python" {
				t.Fatalf("%s: expected a run_python call, got %q", label, name)
			}

			input, _ := obj["input"].(string)
			return input
		}
	}

	t.Fatalf("%s: no custom_tool_call in output (items: %v)", label, kinds)
	return ""
}

// requireFreeform asserts the input reached the client as raw text rather than
// the emulated {"input": ...} wrapper, and that it is syntactically intact.
func requireFreeform(t *testing.T, label, input string) {
	t.Helper()

	if input == "" {
		t.Fatalf("%s: empty tool input", label)
	}

	var wrapper map[string]json.RawMessage

	if err := json.Unmarshal([]byte(input), &wrapper); err == nil {
		if _, leaked := wrapper["input"]; leaked {
			t.Fatalf("%s: the emulated JSON wrapper leaked to the client: %s", label, input)
		}
	}

	requirePython(t, label, input)
}

func requirePython(t *testing.T, label, source string) {
	t.Helper()

	python, err := exec.LookPath("python3")

	if err != nil {
		return
	}

	cmd := exec.Command(python, "-c", `import sys; compile(sys.stdin.read(), "tool.py", "exec")`)
	cmd.Stdin = strings.NewReader(source)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: tool input is not valid Python: %v\n%s\n--- input ---\n%s", label, err, out, source)
	}
}

// TestCustomToolFreeformHTTP covers the non-streaming path: the model receives
// a freeform tool on every backend, and the call comes back as raw text.
func TestCustomToolFreeformHTTP(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	for _, model := range customToolModels() {
		t.Run(model, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model)

			body := map[string]any{
				"model": model,
				"tools": []map[string]any{runPythonTool()},
				"input": escapeHeavyPrompt,
			}

			resp, err := h.Client.Post(ctx, h.Wingman, "/responses", body)

			if err != nil {
				t.Fatalf("request failed: %v", err)
			}

			if resp.StatusCode != 200 {
				t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(resp.RawBody))
			}

			requireFreeform(t, model, customToolCallInput(t, model, resp.Body))
		})
	}
}

// TestCustomToolFreeformSSE covers the streaming path. Emulated backends
// buffer the JSON-wrapped fragments and deliver the freeform text in one
// delta; a native backend streams it incrementally. Either way the deltas must
// sum to the done event and never expose the wrapper.
func TestCustomToolFreeformSSE(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	for _, model := range customToolModels() {
		t.Run(model, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model)

			body := map[string]any{
				"model":  model,
				"stream": true,
				"tools":  []map[string]any{runPythonTool()},
				"input":  `Use run_python once to print("hello — world"). Write nothing else.`,
			}

			events, err := h.Client.PostSSE(ctx, h.Wingman, "/responses", body)

			if err != nil {
				t.Fatalf("request failed: %v", err)
			}

			var deltas strings.Builder
			var done, item string
			var sawDone, sawItem, sawFunctionDelta bool

			for _, e := range events {
				if e.Data == nil {
					continue
				}

				switch e.Data["type"] {
				case "response.custom_tool_call_input.delta":
					text, _ := e.Data["delta"].(string)
					deltas.WriteString(text)

				case "response.custom_tool_call_input.done":
					done, _ = e.Data["input"].(string)
					sawDone = true

				case "response.function_call_arguments.delta":
					sawFunctionDelta = true

				case "response.output_item.done":
					obj, _ := e.Data["item"].(map[string]any)

					if obj != nil && obj["type"] == "custom_tool_call" {
						item, _ = obj["input"].(string)
						sawItem = true
					}
				}
			}

			if sawFunctionDelta {
				t.Errorf("%s: a freeform tool must not stream function_call_arguments deltas", model)
			}

			if !sawDone {
				t.Fatalf("%s: no custom_tool_call_input.done event", model)
			}

			if !sawItem {
				t.Fatalf("%s: no custom_tool_call output_item.done event", model)
			}

			if item != done {
				t.Errorf("%s: output_item.done input does not match the done event:\n item: %q\n done: %q", model, item, done)
			}

			if got := deltas.String(); got != done {
				t.Errorf("%s: input deltas do not sum to the done event:\n deltas: %q\n done:   %q", model, got, done)
			}

			requireFreeform(t, model, done)
		})
	}
}

// TestCustomToolCallReplay is the regression test for the replay path. Before
// the custom-tool emulation existed, a replayed custom_tool_call carried raw
// text that failed json.Unmarshal in the Bedrock and Gemini completers, which
// substituted an empty map — the model was shown a call it had supposedly made
// with no input at all. Asking the model to echo the source back proves the
// input survived the round trip.
func TestCustomToolCallReplay(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	const source = "print(\"alpha — beta\")\nx = r\"C:\\temp\\z.txt\"\n"

	for _, model := range customToolModels() {
		t.Run(model, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model)

			body := map[string]any{
				"model": model,
				"tools": []map[string]any{runPythonTool()},
				"input": []map[string]any{
					{
						"type": "message",
						"role": "user",
						"content": []map[string]any{
							{"type": "input_text", "text": "Run a script that prints alpha — beta."},
						},
					},
					{
						"type":    "custom_tool_call",
						"call_id": "call_replay_1",
						"name":    "run_python",
						"input":   source,
					},
					{
						"type":    "custom_tool_call_output",
						"call_id": "call_replay_1",
						"output":  "alpha — beta",
					},
					{
						"type": "message",
						"role": "user",
						"content": []map[string]any{
							{"type": "input_text", "text": "Repeat back VERBATIM the exact source you passed to run_python, in a fenced code block. Do not call the tool again."},
						},
					},
				},
			}

			resp, err := h.Client.Post(ctx, h.Wingman, "/responses", body)

			if err != nil {
				t.Fatalf("request failed: %v", err)
			}

			if resp.StatusCode != 200 {
				t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(resp.RawBody))
			}

			reply := outputText(resp)

			// The em dash and the backslashes are what a JSON-argument round
			// trip mangles, so they are the parts worth asserting.
			for _, fragment := range []string{`print("alpha — beta")`, `C:\temp\z.txt`} {
				if !strings.Contains(reply, fragment) {
					t.Errorf("%s: replayed input lost %q\n--- reply ---\n%s", model, fragment, reply)
				}
			}
		})
	}
}

func outputText(resp *harness.RawResponse) string {
	var text strings.Builder

	output, _ := resp.Body["output"].([]any)

	for _, item := range output {
		obj, _ := item.(map[string]any)
		content, _ := obj["content"].([]any)

		for _, part := range content {
			block, _ := part.(map[string]any)

			if block["type"] == "output_text" {
				s, _ := block["text"].(string)
				text.WriteString(s)
			}
		}
	}

	return text.String()
}
