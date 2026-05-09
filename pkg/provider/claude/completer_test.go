package claude_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/claude"
)

// TestMain re-executes this binary as a fake `claude` CLI when the
// FAKE_CLAUDE_SCRIPT env var is set. The script is a JSON array of frames the
// fake should emit on stdout once a control_request initialize has been
// received and a user frame has been read. Control requests we initiate
// (mcp_message dispatch) are answered automatically.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_CLAUDE_RUN") == "1" {
		runFakeCLI()
		return
	}
	os.Exit(m.Run())
}

type fakeFrame struct {
	After string          `json:"after"` // "init" | "user" | "previous"
	Frame json.RawMessage `json:"frame"`
}

func runFakeCLI() {
	scriptPath := os.Getenv("FAKE_CLAUDE_SCRIPT")
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake claude: read script:", err)
		os.Exit(1)
	}

	var frames []fakeFrame
	if err := json.Unmarshal(data, &frames); err != nil {
		fmt.Fprintln(os.Stderr, "fake claude: parse script:", err)
		os.Exit(1)
	}

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	emit := func(raw json.RawMessage) {
		out.Write(raw)
		out.WriteString("\n")
		out.Flush()
	}

	emitFor := func(stage string) {
		for _, f := range frames {
			if f.After == stage {
				emit(f.Frame)
			}
		}
	}

	// Stage 0: init system frame.
	emitFor("connect")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)

	stage := "connect"
	requestCounter := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue
		}

		switch env["type"] {
		case "control_request":
			id, _ := env["request_id"].(string)
			req, _ := env["request"].(map[string]any)
			subtype, _ := req["subtype"].(string)

			switch subtype {
			case "initialize":
				resp := map[string]any{
					"type": "control_response",
					"response": map[string]any{
						"subtype":    "success",
						"request_id": id,
						"response":   map[string]any{},
					},
				}
				b, _ := json.Marshal(resp)
				emit(b)

				stage = "init"
				emitFor("init")

			default:
				resp := map[string]any{
					"type": "control_response",
					"response": map[string]any{
						"subtype":    "success",
						"request_id": id,
						"response":   map[string]any{},
					},
				}
				b, _ := json.Marshal(resp)
				emit(b)
			}

		case "user":
			stage = "user"
			emitFor("user")

		case "control_response":
			// Response to one of OUR control_requests (e.g. mcp dispatch).
			// On receipt, advance to next scripted batch.
			requestCounter++
			tag := fmt.Sprintf("after_response_%d", requestCounter)
			emitFor(tag)
			_ = stage
		}
	}
}

// scriptFile materialises a slice of fakeFrame as a temporary JSON file the
// fake CLI will read on startup.
func scriptFile(t *testing.T, frames []fakeFrame) string {
	t.Helper()

	data, err := json.Marshal(frames)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.CreateTemp(t.TempDir(), "fake-claude-script-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}

	return f.Name()
}

func fakeCommand(t *testing.T, frames []fakeFrame) string {
	t.Helper()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	scriptPath := scriptFile(t, frames)

	// The fake CLI ignores all CLI flags ("$@") since it doesn't parse them;
	// the real `claude` flags would otherwise confuse the test binary's flag
	// parser and abort before runFakeCLI() runs.
	wrapper := fmt.Sprintf(`#!/bin/sh
FAKE_CLAUDE_RUN=1 FAKE_CLAUDE_SCRIPT=%q exec %q -test.run=^FAKE_CLAUDE_NEVER_MATCHES$
`, scriptPath, exe)

	wrapperPath := scriptPath + ".sh"
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	return wrapperPath
}

func raw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// makeFrames is a small helper to keep test bodies focused on the protocol
// rather than JSON plumbing.
func makeFrames(t *testing.T, items ...fakeFrame) []fakeFrame {
	t.Helper()
	return items
}

func TestCompleteText(t *testing.T) {
	if !commandExists("sh") {
		t.Skip("requires /bin/sh")
	}

	frames := makeFrames(t,
		fakeFrame{
			After: "connect",
			Frame: raw(t, map[string]any{
				"type":       "system",
				"subtype":    "init",
				"session_id": "sess_1",
				"model":      "claude-test",
			}),
		},
		fakeFrame{
			After: "user",
			Frame: raw(t, map[string]any{
				"type":       "assistant",
				"session_id": "sess_1",
				"message": map[string]any{
					"id":    "msg_1",
					"role":  "assistant",
					"model": "claude-test",
					"content": []map[string]any{
						{"type": "text", "text": "hello world"},
					},
				},
			}),
		},
		fakeFrame{
			After: "user",
			Frame: raw(t, map[string]any{
				"type":          "result",
				"subtype":       "success",
				"is_error":      false,
				"session_id":    "sess_1",
				"stop_reason":   "end_turn",
				"duration_ms":   10,
				"num_turns":     1,
				"usage": map[string]any{"input_tokens": 5, "output_tokens": 2},
			}),
		},
	)

	cmd := fakeCommand(t, frames)

	c, err := claude.NewCompleter("claude-test", claude.WithCommand(cmd))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msgs := []provider.Message{provider.UserMessage("hi")}

	var (
		seenText  string
		final     *provider.Completion
		yieldErr  error
	)

	for completion, err := range c.Complete(ctx, msgs, nil) {
		if err != nil {
			yieldErr = err
			break
		}
		if completion == nil {
			continue
		}
		if completion.Status != "" {
			final = completion
			continue
		}
		if completion.Message != nil {
			for _, content := range completion.Message.Content {
				if content.Text != "" {
					seenText += content.Text
				}
			}
		}
	}

	if yieldErr != nil {
		t.Fatalf("yield error: %v", yieldErr)
	}
	if seenText != "hello world" {
		t.Fatalf("expected text 'hello world', got %q", seenText)
	}
	if final == nil {
		t.Fatal("no final completion received")
	}
	if final.Status != provider.CompletionStatusCompleted {
		t.Errorf("status: got %q", final.Status)
	}
	if final.Usage == nil || final.Usage.InputTokens != 5 || final.Usage.OutputTokens != 2 {
		t.Errorf("usage: %+v", final.Usage)
	}
	if final.Model != "claude-test" {
		t.Errorf("model: got %q", final.Model)
	}
}

func TestCompleteToolCall(t *testing.T) {
	if !commandExists("sh") {
		t.Skip("requires /bin/sh")
	}

	frames := makeFrames(t,
		fakeFrame{
			After: "connect",
			Frame: raw(t, map[string]any{
				"type":       "system",
				"subtype":    "init",
				"session_id": "sess_2",
				"model":      "claude-test",
			}),
		},
		fakeFrame{
			After: "user",
			Frame: raw(t, map[string]any{
				"type":       "assistant",
				"session_id": "sess_2",
				"message": map[string]any{
					"id":    "msg_2",
					"role":  "assistant",
					"model": "claude-test",
					"content": []map[string]any{
						{
							"type":  "tool_use",
							"id":    "toolu_1",
							"name":  "mcp__wingman__echo",
							"input": map[string]any{"value": "ping"},
						},
					},
				},
			}),
		},
		fakeFrame{
			After: "user",
			Frame: raw(t, map[string]any{
				"type":          "result",
				"subtype":       "success",
				"is_error":      false,
				"session_id":    "sess_2",
				"stop_reason":   "tool_use",
				"duration_ms":   10,
				"num_turns":     1,
				"usage":         map[string]any{"input_tokens": 5, "output_tokens": 2},
			}),
		},
	)

	cmd := fakeCommand(t, frames)

	c, err := claude.NewCompleter("claude-test", claude.WithCommand(cmd))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msgs := []provider.Message{provider.UserMessage("call the tool")}
	opts := &provider.CompleteOptions{
		Tools: []provider.Tool{
			{
				Name:        "echo",
				Description: "Echo input back.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{"value": map[string]any{"type": "string"}},
				},
			},
		},
	}

	var (
		toolCalls []provider.ToolCall
		final     *provider.Completion
	)

	for completion, err := range c.Complete(ctx, msgs, opts) {
		if err != nil {
			t.Fatalf("yield error: %v", err)
		}
		if completion == nil {
			continue
		}
		if completion.Status != "" {
			final = completion
			continue
		}
		if completion.Message != nil {
			for _, content := range completion.Message.Content {
				if content.ToolCall != nil {
					toolCalls = append(toolCalls, *content.ToolCall)
				}
			}
		}
	}

	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Name != "echo" {
		t.Errorf("tool name: got %q", toolCalls[0].Name)
	}
	if !strings.Contains(toolCalls[0].Arguments, "ping") {
		t.Errorf("tool args: got %q", toolCalls[0].Arguments)
	}
	if final == nil || final.Status != provider.CompletionStatusCompleted {
		t.Errorf("final completion: %+v", final)
	}
}

func TestErrorResult(t *testing.T) {
	if !commandExists("sh") {
		t.Skip("requires /bin/sh")
	}

	frames := makeFrames(t,
		fakeFrame{
			After: "connect",
			Frame: raw(t, map[string]any{
				"type":       "system",
				"subtype":    "init",
				"session_id": "sess_3",
				"model":      "claude-test",
			}),
		},
		fakeFrame{
			After: "user",
			Frame: raw(t, map[string]any{
				"type":        "result",
				"subtype":     "error_during_execution",
				"is_error":    true,
				"session_id":  "sess_3",
				"errors":      []string{"upstream timeout"},
				"duration_ms": 10,
				"num_turns":   1,
			}),
		},
	)

	cmd := fakeCommand(t, frames)

	c, err := claude.NewCompleter("claude-test", claude.WithCommand(cmd))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msgs := []provider.Message{provider.UserMessage("hi")}

	var sawError bool

	for _, err := range c.Complete(ctx, msgs, nil) {
		if err != nil {
			sawError = true
			var pe *provider.ProviderError
			if !errAs(err, &pe) {
				t.Fatalf("expected ProviderError, got %T: %v", err, err)
			}
			if !strings.Contains(pe.Message, "upstream timeout") {
				t.Errorf("message: got %q", pe.Message)
			}
			break
		}
	}

	if !sawError {
		t.Fatal("expected an error from the iterator")
	}
}

// commandExists reports whether the given executable resolves on PATH.
func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func errAs(err error, target any) bool {
	type asI interface {
		As(any) bool
	}
	_ = asI(nil)
	// Minimal local errors.As to avoid an extra import.
	return errorsAs(err, target)
}

func errorsAs(err error, target any) bool {
	for err != nil {
		if assignTarget(err, target) {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func assignTarget(err error, target any) bool {
	switch tgt := target.(type) {
	case **provider.ProviderError:
		if pe, ok := err.(*provider.ProviderError); ok {
			*tgt = pe
			return true
		}
	}
	return false
}
