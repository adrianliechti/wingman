package claude

import (
	"encoding/base64"
	"encoding/json"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// convertCliContent maps a CLI assistant content block to the provider type.
// Returns ok=false for blocks that have no caller-visible payload.
func convertCliContent(block cliContent) (provider.Content, bool) {
	switch block.Type {
	case "text":
		if block.Text == "" {
			return provider.Content{}, false
		}
		return provider.TextContent(block.Text), true

	case "thinking":
		if block.Thinking == "" && block.Signature == "" {
			return provider.Content{}, false
		}
		return provider.ReasoningContent(provider.Reasoning{
			Text:      block.Thinking,
			Signature: block.Signature,
		}), true

	case "tool_use":
		args := string(block.Input)
		if args == "" {
			args = "{}"
		}
		return provider.ToolCallContent(provider.ToolCall{
			ID:        block.ID,
			Name:      stripToolPrefix(block.Name),
			Arguments: args,
		}), true

	case "tool_result":
		return provider.ToolResultContent(provider.ToolResult{
			ID:    block.ToolUseID,
			Parts: decodeToolResultContent(block.ResultData),
		}), true

	case "refusal":
		if block.Refusal == "" {
			return provider.Content{}, false
		}
		return provider.RefusalContent(block.Refusal), true
	}

	return provider.Content{}, false
}

// decodeToolResultContent parses the CLI's polymorphic tool_result content
// field (either a JSON string or an array of typed blocks) into Parts so
// image / document blocks survive round-trips.
func decodeToolResultContent(raw json.RawMessage) []provider.Part {
	if len(raw) == 0 {
		return nil
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil
		}
		return []provider.Part{{Text: s}}
	}

	var blocks []cliContent
	if err := json.Unmarshal(raw, &blocks); err != nil {
		// Unknown shape — preserve raw JSON as text so callers can debug.
		return []provider.Part{{Text: string(raw)}}
	}

	var parts []provider.Part
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				parts = append(parts, provider.Part{Text: b.Text})
			}
		case "image", "document":
			if b.Source == nil {
				continue
			}
			file := &provider.File{ContentType: b.Source.MediaType}
			if b.Source.Type == "base64" {
				if data, err := base64.StdEncoding.DecodeString(b.Source.Data); err == nil {
					file.Content = data
				}
			}
			parts = append(parts, provider.Part{File: file})
		}
	}
	return parts
}

// yieldContent yields a single content block as a Completion delta. Returns
// false if the consumer cancelled the iterator.
func yieldContent(yield func(*provider.Completion, error) bool, id, model string, content provider.Content) bool {
	delta := &provider.Completion{
		ID:    id,
		Model: model,
		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: []provider.Content{content},
		},
	}
	return yield(delta, nil)
}
