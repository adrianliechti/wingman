package claudecode

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
)

func checkExchanges(t *testing.T, exchanges []exchange, model string) []string {
	t.Helper()
	emitted, returned := map[string]string{}, map[string]bool{}
	succeeded, failed := map[string]bool{}, map[string]bool{}
	messages := 0
	for i, record := range exchanges {
		u, err := url.Parse(record.Path)
		if err != nil || (u.Path != "/v1/messages" && u.Path != "/v1/messages/count_tokens") {
			continue // Capture optional gateway probes, but only require the API contract.
		}
		if u.Path == "/v1/messages" {
			messages++
		}
		if record.Status != 200 {
			t.Errorf("exchange %d: %s returned %d: %.2000s", i, record.Path, record.Status, record.Response)
			continue
		}
		if u.Path != "/v1/messages" {
			continue
		}
		var request struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal([]byte(record.Request), &request); err != nil {
			t.Errorf("exchange %d: invalid request JSON: %v", i, err)
			continue
		}
		if record.Method != "POST" || request.Model != model || !request.Stream || record.RequestHeaders.Get("Anthropic-Version") == "" {
			t.Errorf("exchange %d: unexpected Messages request (method=%s model=%s stream=%t version=%s)", i, record.Method, request.Model, request.Stream, record.RequestHeaders.Get("Anthropic-Version"))
		}
		for _, message := range request.Messages {
			if message.Role != "user" {
				continue
			}
			var blocks []struct {
				Type      string `json:"type"`
				ToolUseID string `json:"tool_use_id"`
				IsError   bool   `json:"is_error"`
			}
			if json.Unmarshal(message.Content, &blocks) != nil {
				continue // User content can also be a string.
			}
			for _, block := range blocks {
				if block.Type != "tool_result" {
					continue
				}
				if emitted[block.ToolUseID] == "" {
					t.Errorf("exchange %d: unmatched tool result %q", i, block.ToolUseID)
				}
				returned[block.ToolUseID] = true
				// Models may retry a valid tool call after a local file or
				// argument error. Count only successful calls toward coverage.
				if block.IsError {
					failed[block.ToolUseID] = true
				} else {
					succeeded[block.ToolUseID] = true
				}
			}
		}
		if !strings.HasPrefix(record.ResponseHeaders.Get("Content-Type"), "text/event-stream") {
			t.Errorf("exchange %d: expected SSE, got %s", i, record.ResponseHeaders.Get("Content-Type"))
			continue
		}
		tools, err := validateStream(record.Response)
		if err != nil {
			t.Errorf("exchange %d: %v", i, err)
		}
		for id, name := range tools {
			if emitted[id] != "" {
				t.Errorf("exchange %d: reused tool-use ID %q", i, id)
			}
			emitted[id] = name
		}
	}
	if messages == 0 {
		t.Error("Claude Code made no Messages requests through the recorder")
	}
	names := map[string]bool{}
	for id, name := range emitted {
		if !returned[id] {
			t.Errorf("tool call %s (%s) was not returned in a later request", name, id)
		}
		if succeeded[id] {
			names[name] = true
		}
	}
	var result []string
	for name := range names {
		result = append(result, name)
	}
	slices.Sort(result)
	t.Logf("validated %d Messages streams and %d tool calls (%d tool errors)", messages, len(emitted), len(failed))
	return result
}

// Check lifecycle and tool JSON without depending on how a provider chunks
// text or arguments. Both the reference and Wingman must obey this contract.
func validateStream(raw string) (map[string]string, error) {
	events, err := harness.ParseSSE(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}
	type block struct {
		kind, id, name string
		input          any
		partial        strings.Builder
		closed         bool
	}
	blocks := map[float64]*block{}
	tools := map[string]string{}
	started, stopped, delta := false, false, false
	for _, event := range events {
		data := event.Data
		if data == nil || data["type"] != event.Event {
			return nil, fmt.Errorf("invalid SSE data for %q", event.Event)
		}
		if event.Event == "ping" {
			continue
		}
		if stopped || (!started && event.Event != "message_start") {
			return nil, fmt.Errorf("out-of-order %s", event.Event)
		}
		index, hasIndex := data["index"].(float64)
		b := blocks[index]
		switch event.Event {
		case "message_start":
			message, _ := data["message"].(map[string]any)
			if started || message["type"] != "message" || message["role"] != "assistant" || message["id"] == "" || message["id"] == nil || !hasUsage(message["usage"], "input_tokens", "output_tokens") {
				return nil, fmt.Errorf("invalid message_start or missing usage")
			}
			started = true
		case "content_block_start":
			content, _ := data["content_block"].(map[string]any)
			if delta || !hasIndex || index < 0 || index != float64(int(index)) || b != nil || content == nil {
				return nil, fmt.Errorf("invalid content_block_start at %v", data["index"])
			}
			b = &block{input: content["input"]}
			b.kind, _ = content["type"].(string)
			b.id, _ = content["id"].(string)
			b.name, _ = content["name"].(string)
			blocks[index] = b
		case "content_block_delta":
			if !hasIndex || b == nil || b.closed || delta {
				return nil, fmt.Errorf("delta outside an open content block")
			}
			d, _ := data["delta"].(map[string]any)
			if d["type"] == "input_json_delta" {
				text, ok := d["partial_json"].(string)
				if !ok || b.kind != "tool_use" {
					return nil, fmt.Errorf("invalid tool input delta")
				}
				b.partial.WriteString(text)
			}
		case "content_block_stop":
			if !hasIndex || b == nil || b.closed {
				return nil, fmt.Errorf("stop outside an open content block")
			}
			b.closed = true
			if b.kind == "tool_use" {
				input, _ := b.input.(map[string]any)
				if b.partial.Len() > 0 {
					if err := json.Unmarshal([]byte(b.partial.String()), &input); err != nil {
						return nil, fmt.Errorf("invalid streamed tool JSON: %w", err)
					}
				}
				if b.id == "" || b.name == "" || input == nil || tools[b.id] != "" {
					return nil, fmt.Errorf("invalid tool_use block")
				}
				tools[b.id] = b.name
			}
		case "message_delta":
			d, _ := data["delta"].(map[string]any)
			if delta || d["stop_reason"] == "" || d["stop_reason"] == nil || !hasUsage(data["usage"], "output_tokens") {
				return nil, fmt.Errorf("invalid message_delta or missing usage")
			}
			for _, b := range blocks {
				if !b.closed {
					return nil, fmt.Errorf("message_delta before content_block_stop")
				}
			}
			delta = true
		case "message_stop":
			if !delta {
				return nil, fmt.Errorf("message_stop before message_delta")
			}
			stopped = true
		case "error":
			return nil, fmt.Errorf("SSE error: %s", event.Raw)
		default:
			return nil, fmt.Errorf("unexpected SSE event %q", event.Event)
		}
	}
	if !stopped {
		return nil, fmt.Errorf("incomplete SSE stream")
	}
	return tools, nil
}

func hasUsage(value any, fields ...string) bool {
	usage, _ := value.(map[string]any)
	for _, field := range fields {
		n, ok := usage[field].(float64)
		if !ok || n < 0 {
			return false
		}
	}
	return true
}
