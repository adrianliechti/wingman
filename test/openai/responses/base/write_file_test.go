package base_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/responses"
)

// Reproduces intermittent write_file tool failures: with large, escape-heavy
// arguments (a complex Python script as `content`), the streamed function call
// sometimes breaks mid-way or reassembles into invalid JSON.
//
// Defaults to bedrock-sonnet-4-6; crank iterations to hunt the flake:
//
//	TEST_WRITE_FILE_ITERATIONS=10 go test ./test/openai/responses/base -run TestWriteFileComplexPython -v -timeout 60m
//
// tool_choice is intentionally not set, matching the real client: the model
// decides to call create_file (the prompt demands it), and thinking stays
// enabled on Anthropic models — interleaved thinking blocks are part of the
// stream shape wingman has to reassemble.

// Verbatim copy of the production create_file tool
// (wingman-chat src/shared/lib/file-tools.ts) — keep in sync. The web client
// intentionally sends this schema-guided (strict=false), so this probe must do
// the same: strict mode materially changes how Claude authors tool arguments.
var writeFileTool = map[string]any{
	"type":        "function",
	"name":        "create_file",
	"strict":      false,
	"description": "Create a new file or update an existing file with the specified path and content. Recognized structured formats are saved first, then validated; validation errors are reported so you can continue editing and retry.",
	"parameters": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path (e.g., /data/output.csv). Should start with /.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The content of the file to create.",
			},
		},
		"required":             []string{"path", "content"},
		"additionalProperties": false,
	},
}

// Verbatim schema and strictness from wingman-chat's
// PYTHON_EXECUTION_PARAMETERS. The production handler accepts either path or
// inline code, so neither selector is schema-required; this is precisely the
// loose shape involved when the browser reports that no code was received.
var executePythonTool = map[string]any{
	"type":   "function",
	"name":   "execute_python_code",
	"strict": false,
	"description": "Execute Python code when the task requires computation, programmatic file processing, " +
		"transformation, batch work, or file generation. Pass the full script body in code, or path to run an existing .py artifact.",
	"parameters": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to an existing Python artifact (for example, /analysis.py). Omit when using inline code. Ignored when code is non-empty.",
			},
			"code": map[string]any{
				"type":        "string",
				"description": "Inline Python code to execute. Omit when running an existing script via path.",
			},
		},
		"required":             []string{},
		"additionalProperties": false,
	},
}

const parallelPayloadPrompt = `Emit exactly two independent function calls in this single assistant response, with no prose before, between, or after them. Do not wait for either tool result.

1. Call create_file exactly once with path "/parallel/generated_report.py" and content containing a complete, syntactically valid Python program of at least 1,200 characters. It must start with the comment "# PARALLEL_FILE_SENTINEL", use dataclasses, json, regexes, f-strings, nested dictionaries, Unicode text, and a Windows path, then print a JSON report from main().
2. Call execute_python_code exactly once with inline code of at least 800 characters. The code must start with the comment "# PARALLEL_CODE_SENTINEL", independently construct and print a JSON summary using nested quotes, regexes, Unicode, and backslashes. Omit path.

The two calls are independent and must both be emitted now. Never call any tool whose name starts with unused_fixture_.`

// Keep a realistically broad toolbox around the two production-shaped tools.
// The decoys isolate whether argument state changes merely because many tools
// are advertised, without copying unrelated web-client implementation details
// into this protocol test.
func parallelPayloadTools() []any {
	tools := []any{writeFileTool, executePythonTool}
	for i := 1; i <= 12; i++ {
		tools = append(tools, map[string]any{
			"type":        "function",
			"name":        fmt.Sprintf("unused_fixture_%02d", i),
			"strict":      false,
			"description": "Compatibility fixture only. Do not call this tool.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
				},
				"required":             []string{},
				"additionalProperties": false,
			},
		})
	}
	return tools
}

// The prompt demands docstrings, f-strings, quotes, unicode, embedded
// newlines, regexes (raw and non-raw) and Windows paths — content whose JSON
// encoding is dominated by escape sequences and backslash ambiguity, the
// escaping decisions models actually get wrong.
const writeFilePrompt = `Create the file "/report/generate_deck.py" using the create_file tool.

The file must be one complete, runnable Python script using python-pptx that builds a 7-slide quarterly business report:

1. Title slide with title, subtitle and date.
2. Agenda slide with a bulleted list.
3. KPI slide: a 4x5 table with header formatting and per-cell font sizes.
4. Chart slide: clustered bar chart via CategoryChartData with three data series.
5. Regional results slide: text frames with bold runs and colored runs (RGBColor).
6. Risks slide with nested bullet levels.
7. Closing slide.

The code must include:
- a module docstring using triple quotes
- type hints and f-strings
- a CONFIG dict whose string values contain double quotes, apostrophes, umlauts (ä, ö, ü), em dashes (—) and embedded \n newlines
- speaker notes on every slide, some containing quoted phrases like "Q3 wasn't easy — but we delivered."
- helper functions add_title_slide(), add_table_slide(), add_chart_slide()
- a validate_date() helper using the re module with BOTH a raw-string regex (like re.compile(r"\d{4}-\d{2}-\d{2}")) and an equivalent non-raw-string regex (like "\\d{4}-\\d{2}-\\d{2}")
- a CONFIG entry "legacy_export_path" holding a Windows-style path such as C:\temp\reports\out.pptx
- a CONFIG entry whose value documents the literal two-character sequence \n (backslash + n) while also containing a real newline
- a main() entry point guarded by if __name__ == "__main__"

Write the ENTIRE script in exactly one create_file call. Do not reply in plain text and do not split the file across multiple calls.`

func writeFileModels() []string {
	if v := os.Getenv("TEST_WRITE_FILE_MODELS"); v != "" {
		var names []string
		for s := range strings.SplitSeq(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				names = append(names, s)
			}
		}
		return names
	}
	return []string{"bedrock-sonnet-4-6"}
}

func writeFileIterations() int {
	if v := os.Getenv("TEST_WRITE_FILE_ITERATIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 3
}

func writeFileBody(model string, stream bool) map[string]any {
	return responses.WithModel(map[string]any{
		"stream": stream,
		"input":  writeFilePrompt,
		"tools":  []any{writeFileTool},
	}, model)
}

func TestWriteFileComplexPythonHTTP(t *testing.T) {
	h := openai.New(t)
	h.Client.Timeout = 5 * time.Minute
	ctx := context.Background()

	for _, model := range writeFileModels() {
		t.Run(model, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model)

			for i := 1; i <= writeFileIterations(); i++ {
				t.Run(fmt.Sprintf("run-%02d", i), func(t *testing.T) {
					resp, err := h.Client.Post(ctx, h.Wingman, "/responses", writeFileBody(model, false))
					if err != nil {
						t.Fatalf("wingman request failed: %v", err)
					}
					if resp.StatusCode != 200 {
						t.Fatalf("wingman returned status %d: %s", resp.StatusCode, string(resp.RawBody))
					}

					requireCompletedStatus(t, resp.Body)

					args := extractWriteFileArgumentsHTTP(t, resp.Body)
					requireWriteFileArguments(t, args, false)
				})
			}
		})
	}
}

func TestWriteFileComplexPythonSSE(t *testing.T) {
	h := openai.New(t)
	h.Client.Timeout = 5 * time.Minute
	ctx := context.Background()

	for _, model := range writeFileModels() {
		t.Run(model, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model)

			for i := 1; i <= writeFileIterations(); i++ {
				t.Run(fmt.Sprintf("run-%02d", i), func(t *testing.T) {
					events, err := h.Client.PostSSE(ctx, h.Wingman, "/responses", writeFileBody(model, true))
					if err != nil {
						t.Fatalf("wingman SSE request failed: %v", err)
					}

					args, transportVerified := extractWriteFileArgumentsSSE(t, events)
					requireWriteFileArguments(t, args, transportVerified)
				})
			}
		})
	}
}

// TestWriteFileTruncationSSE deterministically reproduces the browser symptom:
// a long create_file call reaches max_output_tokens after the function item has
// started but before its JSON arguments are complete. The Responses contract
// marks both the item and response incomplete and deliberately omits
// function_call_arguments.done. A client must not parse or execute this item.
func TestWriteFileTruncationSSE(t *testing.T) {
	h := openai.New(t)
	h.Client.Timeout = 5 * time.Minute
	ctx := context.Background()

	for _, model := range writeFileModels() {
		t.Run(model, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model)

			events, err := h.Client.PostSSE(ctx, h.Wingman, "/responses", responses.WithModel(map[string]any{
				"stream":            true,
				"max_output_tokens": 900,
				"input":             writeFilePrompt,
				"tools":             []any{writeFileTool},
			}, model))
			if err != nil {
				t.Fatalf("wingman SSE request failed: %v", err)
			}

			requireTruncatedFunctionCall(t, events, "create_file")
		})
	}
}

func requireTruncatedFunctionCall(t *testing.T, events []*harness.SSEEvent, wantName string) {
	t.Helper()

	var deltas strings.Builder
	var itemDoneArgs string
	var callStarted, argsDone, itemDone, responseIncomplete, responseCompleted bool

	for _, event := range events {
		if event.Data == nil {
			continue
		}

		switch event.Data["type"] {
		case "response.output_item.added":
			item, _ := event.Data["item"].(map[string]any)
			if item["type"] == "function_call" && item["name"] == wantName {
				callStarted = true
			}

		case "response.function_call_arguments.delta":
			delta, _ := event.Data["delta"].(string)
			deltas.WriteString(delta)

		case "response.function_call_arguments.done":
			argsDone = true

		case "response.output_item.done":
			item, _ := event.Data["item"].(map[string]any)
			if item["type"] != "function_call" || item["name"] != wantName {
				continue
			}
			itemDone = true
			if status, _ := item["status"].(string); status != "incomplete" {
				t.Errorf("truncated function item status = %q, want incomplete", status)
			}
			itemDoneArgs, _ = item["arguments"].(string)

		case "response.incomplete":
			responseIncomplete = true
			response, _ := event.Data["response"].(map[string]any)
			if status, _ := response["status"].(string); status != "incomplete" {
				t.Errorf("terminal response status = %q, want incomplete", status)
			}
			details, _ := response["incomplete_details"].(map[string]any)
			if reason, _ := details["reason"].(string); reason != "max_output_tokens" {
				t.Errorf("incomplete reason = %q, want max_output_tokens", reason)
			}

		case "response.completed":
			responseCompleted = true
		}
	}

	if !callStarted {
		t.Fatal("token cap was reached before create_file started; raise max_output_tokens enough to exercise partial arguments")
	}
	if deltas.Len() == 0 {
		t.Fatal("create_file started but streamed no argument bytes")
	}
	if argsDone {
		t.Error("truncated arguments incorrectly emitted function_call_arguments.done")
	}
	if !itemDone {
		t.Fatal("truncated function call has no output_item.done")
	}
	if itemDoneArgs != deltas.String() {
		t.Fatalf("incomplete output_item.done did not preserve partial deltas: item len=%d, deltas len=%d", len(itemDoneArgs), deltas.Len())
	}
	if !responseIncomplete {
		t.Fatal("stream has no response.incomplete terminal event")
	}
	if responseCompleted {
		t.Error("stream emitted response.completed after response.incomplete")
	}

	var parsed map[string]any
	if json.Unmarshal([]byte(deltas.String()), &parsed) == nil {
		if content, ok := parsed["content"].(string); ok && content != "" {
			t.Fatalf("900-token fixture unexpectedly completed content (%d chars); lower max_output_tokens to retain the truncation repro", len(content))
		}
	}
}

// TestParallelLargePayloadToolsSSE exercises the two browser tools that have
// reported empty payloads in the same response. It also advertises enough
// decoy tools to catch indexing/state bugs that only appear with a broad
// toolbox. Every call is cross-checked at all three wire representations:
// argument deltas, arguments.done, and output_item.done.
func TestParallelLargePayloadToolsSSE(t *testing.T) {
	h := openai.New(t)
	h.Client.Timeout = 5 * time.Minute
	ctx := context.Background()

	for _, model := range writeFileModels() {
		t.Run(model, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model)

			events, err := h.Client.PostSSE(ctx, h.Wingman, "/responses", responses.WithModel(map[string]any{
				"stream":              true,
				"parallel_tool_calls": true,
				"input":               parallelPayloadPrompt,
				"tools":               parallelPayloadTools(),
			}, model))
			if err != nil {
				t.Fatalf("wingman SSE request failed: %v", err)
			}

			calls := extractFunctionCallStreams(t, events)
			if len(calls) != 2 {
				names := make([]string, len(calls))
				for i, call := range calls {
					names[i] = call.name
				}
				t.Fatalf("expected exactly two parallel function calls, got %d: %v", len(calls), names)
			}

			seen := map[string]bool{}
			for _, call := range calls {
				seen[call.name] = true
				switch call.name {
				case "create_file":
					var args struct {
						Path    string `json:"path"`
						Content string `json:"content"`
					}
					if err := json.Unmarshal([]byte(call.arguments()), &args); err != nil {
						t.Fatalf("create_file arguments are invalid JSON: %v\n%s", err, argsTail(call.arguments()))
					}
					if args.Path != "/parallel/generated_report.py" {
						t.Errorf("create_file path = %q", args.Path)
					}
					if len(args.Content) < 500 || !strings.Contains(args.Content, "PARALLEL_FILE_SENTINEL") {
						t.Fatalf("create_file content is missing/short (%d chars): %q", len(args.Content), argsTail(args.Content))
					}

				case "execute_python_code":
					var args struct {
						Path string `json:"path"`
						Code string `json:"code"`
					}
					if err := json.Unmarshal([]byte(call.arguments()), &args); err != nil {
						t.Fatalf("execute_python_code arguments are invalid JSON: %v\n%s", err, argsTail(call.arguments()))
					}
					if args.Path != "" {
						t.Errorf("execute_python_code unexpectedly set path = %q", args.Path)
					}
					if len(args.Code) < 300 || !strings.Contains(args.Code, "PARALLEL_CODE_SENTINEL") {
						t.Fatalf("execute_python_code code is missing/short (%d chars): %q", len(args.Code), argsTail(args.Code))
					}

				default:
					t.Fatalf("model called unexpected tool %q", call.name)
				}
			}

			if !seen["create_file"] || !seen["execute_python_code"] {
				t.Fatalf("missing required calls: seen=%v", seen)
			}
		})
	}
}

type streamedFunctionCall struct {
	itemID      string
	name        string
	outputIndex int
	deltas      strings.Builder
	done        string
	doneSeen    bool
	itemDone    string
	itemSeen    bool
}

func (c *streamedFunctionCall) arguments() string {
	return c.deltas.String()
}

// extractFunctionCallStreams mirrors the web client's output-index tracking,
// while keying the integrity checks by item_id. A duplicate output index or an
// authoritative terminal item that erases good deltas is reported directly.
func extractFunctionCallStreams(t *testing.T, events []*harness.SSEEvent) []*streamedFunctionCall {
	t.Helper()

	byID := map[string]*streamedFunctionCall{}
	byOutputIndex := map[int]string{}
	var order []*streamedFunctionCall
	completed := false

	indexOf := func(data map[string]any) int {
		value, ok := data["output_index"].(float64)
		if !ok {
			t.Fatalf("event %q has no numeric output_index", data["type"])
		}
		return int(value)
	}
	get := func(id string, outputIndex int) *streamedFunctionCall {
		if id == "" {
			t.Fatal("function-call event has an empty item_id")
		}
		if call := byID[id]; call != nil {
			if call.outputIndex != outputIndex {
				t.Fatalf("item %s moved from output_index %d to %d", id, call.outputIndex, outputIndex)
			}
			return call
		}
		if previous, exists := byOutputIndex[outputIndex]; exists && previous != id {
			t.Fatalf("output_index %d reused by function calls %s and %s", outputIndex, previous, id)
		}
		call := &streamedFunctionCall{itemID: id, outputIndex: outputIndex}
		byID[id] = call
		byOutputIndex[outputIndex] = id
		order = append(order, call)
		return call
	}

	for _, event := range events {
		if event.Data == nil {
			continue
		}

		switch event.Data["type"] {
		case "response.output_item.added":
			item, _ := event.Data["item"].(map[string]any)
			if item["type"] != "function_call" {
				continue
			}
			id, _ := item["id"].(string)
			call := get(id, indexOf(event.Data))
			call.name, _ = item["name"].(string)

		case "response.function_call_arguments.delta":
			id, _ := event.Data["item_id"].(string)
			call := get(id, indexOf(event.Data))
			delta, _ := event.Data["delta"].(string)
			call.deltas.WriteString(delta)

		case "response.function_call_arguments.done":
			id, _ := event.Data["item_id"].(string)
			call := get(id, indexOf(event.Data))
			call.done, _ = event.Data["arguments"].(string)
			call.doneSeen = true
			if call.name == "" {
				call.name, _ = event.Data["name"].(string)
			}

		case "response.output_item.done":
			item, _ := event.Data["item"].(map[string]any)
			if item["type"] != "function_call" {
				continue
			}
			id, _ := item["id"].(string)
			call := get(id, indexOf(event.Data))
			call.itemDone, _ = item["arguments"].(string)
			call.itemSeen = true
			if call.name == "" {
				call.name, _ = item["name"].(string)
			}

		case "response.completed":
			completed = true

		case "response.incomplete":
			t.Fatal("response became incomplete while emitting parallel tool arguments")

		case "response.failed", "error":
			raw, _ := json.Marshal(event.Data)
			t.Fatalf("response stream failed: %s", raw)
		}
	}

	if !completed {
		t.Fatal("stream ended without response.completed")
	}
	for _, call := range order {
		args := call.arguments()
		if args == "" {
			t.Fatalf("%s (%s) streamed no arguments", call.name, call.itemID)
		}
		if !call.doneSeen {
			t.Fatalf("%s (%s) has no function_call_arguments.done", call.name, call.itemID)
		}
		if call.done != args {
			t.Fatalf("%s arguments.done differs from deltas: done len=%d, deltas len=%d", call.name, len(call.done), len(args))
		}
		if !call.itemSeen {
			t.Fatalf("%s (%s) has no output_item.done", call.name, call.itemID)
		}
		if call.itemDone != args {
			t.Fatalf("%s output_item.done differs from deltas: item len=%d, deltas len=%d", call.name, len(call.itemDone), len(args))
		}
	}
	return order
}

func requireCompletedStatus(t *testing.T, body map[string]any) {
	t.Helper()

	status, _ := body["status"].(string)
	if status == "incomplete" {
		details, _ := json.Marshal(body["incomplete_details"])
		t.Fatalf("response incomplete — the tool call was cut off by the token limit, not a streaming bug: %s", details)
	}
	if status != "completed" {
		t.Fatalf("unexpected response status %q (want completed)", status)
	}
}

// extractWriteFileArgumentsHTTP pulls the write_file function_call output item
// from a non-streaming response.
func extractWriteFileArgumentsHTTP(t *testing.T, body map[string]any) string {
	t.Helper()

	output, _ := body["output"].([]any)

	for _, item := range output {
		obj, _ := item.(map[string]any)
		if obj["type"] != "function_call" {
			continue
		}

		if name, _ := obj["name"].(string); name != "create_file" {
			t.Fatalf("expected function_call create_file, got %q", name)
		}

		args, _ := obj["arguments"].(string)
		return args
	}

	raw, _ := json.Marshal(body["output"])
	t.Fatalf("no function_call output item found in response: %s", argsTail(string(raw)))
	return ""
}

// extractWriteFileArgumentsSSE reassembles function_call_arguments.delta
// fragments keyed by item_id and cross-checks them against the terminal
// arguments.done and output_item.done payloads. A stream that breaks mid-way
// (no terminal events) or drops fragments (done != reassembled deltas) fails
// here with a precise diagnosis. The returned bool reports whether all
// cross-checks fully verified transport integrity — when true, any remaining
// JSON defect was authored by the model, not introduced by wingman.
func extractWriteFileArgumentsSSE(t *testing.T, events []*harness.SSEEvent) (string, bool) {
	t.Helper()

	deltas := map[string]*strings.Builder{}
	done := map[string]string{}
	var order []string

	var itemName string
	var itemDoneArgs *string
	var completed, incomplete bool

	for _, e := range events {
		if e.Data == nil {
			continue
		}

		switch e.Data["type"] {
		case "response.output_item.added":
			if item, _ := e.Data["item"].(map[string]any); item["type"] == "function_call" {
				itemName, _ = item["name"].(string)
			}

		case "response.function_call_arguments.delta":
			id, _ := e.Data["item_id"].(string)
			if deltas[id] == nil {
				deltas[id] = &strings.Builder{}
				order = append(order, id)
			}
			d, _ := e.Data["delta"].(string)
			deltas[id].WriteString(d)

		case "response.function_call_arguments.done":
			id, _ := e.Data["item_id"].(string)
			a, _ := e.Data["arguments"].(string)
			done[id] = a

		case "response.output_item.done":
			if item, _ := e.Data["item"].(map[string]any); item["type"] == "function_call" {
				a, _ := item["arguments"].(string)
				itemDoneArgs = &a
			}

		case "response.completed":
			completed = true

		case "response.incomplete":
			incomplete = true
		}
	}

	if incomplete {
		t.Fatal("stream ended with response.incomplete — the tool call was cut off by the token limit, not a streaming bug")
	}

	if len(order) == 0 {
		t.Fatalf("no function_call_arguments.delta events found in stream (%d events total)", len(events))
	}

	if len(order) > 1 {
		t.Errorf("expected a single function_call, got %d — the file may have been split across calls", len(order))
	}

	id := order[0]
	args := deltas[id].String()

	if !completed {
		t.Errorf("stream broke mid-way: no terminal response.completed event\n  reassembled length=%d tail=%q", len(args), argsTail(args))
	}

	doneArgs, doneSeen := done[id]
	if !doneSeen {
		t.Errorf("stream broke mid-way: no function_call_arguments.done event")
	} else if doneArgs != args {
		t.Fatalf("function_call_arguments.done does not match reassembled deltas (dropped/duplicated fragment — wingman transport bug):\n  done   len=%d tail=%q\n  deltas len=%d tail=%q\n  saved to %s", len(doneArgs), argsTail(doneArgs), len(args), argsTail(args), saveFailedArgs(t, "done:\n"+doneArgs+"\n\ndeltas:\n"+args))
	}

	if itemDoneArgs != nil && *itemDoneArgs != args {
		t.Fatalf("output_item.done arguments do not match reassembled deltas (wingman transport bug):\n  item   len=%d tail=%q\n  deltas len=%d tail=%q", len(*itemDoneArgs), argsTail(*itemDoneArgs), len(args), argsTail(args))
	}

	if itemName != "create_file" {
		t.Errorf("expected function_call create_file, got %q", itemName)
	}

	verified := completed && doneSeen && itemDoneArgs != nil

	return args, verified
}

func requireWriteFileArguments(t *testing.T, args string, transportVerified bool) {
	t.Helper()

	var parsed struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}

	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		if transportVerified {
			t.Fatalf("model emitted malformed JSON arguments (stream cross-checks passed, transport is intact — this is the model-authored case a client-side fixer must handle): %v\n  length=%d\n  tail=%q\n  saved to %s", err, len(args), argsTail(args), saveFailedArgs(t, args))
		}
		t.Fatalf("tool arguments are not valid JSON and stream integrity could not be verified (possible truncation or transport bug): %v\n  length=%d\n  tail=%q\n  saved to %s", err, len(args), argsTail(args), saveFailedArgs(t, args))
	}

	if parsed.Path == "" {
		t.Fatalf("arguments missing 'path':\n%s", argsTail(args))
	}

	if len(parsed.Content) < 1500 {
		t.Fatalf("suspiciously short 'content' (%d chars) for the requested script — saved to %s", len(parsed.Content), saveFailedArgs(t, args))
	}

	if !strings.Contains(parsed.Content, "pptx") {
		t.Fatalf("'content' does not look like the requested python-pptx script — saved to %s", saveFailedArgs(t, args))
	}

	requirePythonSyntax(t, parsed.Content)
}

// requirePythonSyntax catches corruption that survives JSON parsing, e.g. a
// fragment dropped inside the content string. Skipped when python3 is absent.
func requirePythonSyntax(t *testing.T, content string) {
	t.Helper()

	python, err := exec.LookPath("python3")
	if err != nil {
		return
	}

	cmd := exec.Command(python, "-c", `import sys; compile(sys.stdin.read(), "generated.py", "exec")`)
	cmd.Stdin = strings.NewReader(content)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated 'content' is not valid Python: %v\n%s  saved to %s\n  Inspect the file: a random mid-token cut means stream corruption; plausible-looking mistakes (e.g. unescaped nested quotes) mean the model authored broken code.", err, out, saveFailedArgs(t, content))
	}
}

func saveFailedArgs(t *testing.T, data string) string {
	t.Helper()

	f, err := os.CreateTemp("", "write_file_args_*.txt")
	if err != nil {
		return "(failed to save: " + err.Error() + ")"
	}
	defer f.Close()

	if _, err := f.WriteString(data); err != nil {
		return "(failed to save: " + err.Error() + ")"
	}
	return f.Name()
}
