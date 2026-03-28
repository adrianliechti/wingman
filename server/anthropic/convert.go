package anthropic

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/tool"
)

func toMessages(system string, messages []MessageParam) ([]provider.Message, error) {
	var result []provider.Message

	if system != "" {
		result = append(result, provider.SystemMessage(system))
	}

	for _, m := range messages {
		message, err := toMessage(m)

		if err != nil {
			return nil, err
		}

		result = append(result, *message)
	}

	return result, nil
}

func toMessage(m MessageParam) (*provider.Message, error) {
	blocks, err := parseContentBlocks(m.Content)

	if err != nil {
		return nil, err
	}

	var role provider.MessageRole

	switch m.Role {
	case MessageRoleUser:
		role = provider.MessageRoleUser

	case MessageRoleAssistant:
		role = provider.MessageRoleAssistant

	default:
		role = provider.MessageRoleUser
	}

	var content []provider.Content

	for _, block := range blocks {
		switch block.Type {
		case "text":
			content = append(content, provider.TextContent(block.Text))

		case "image":
			if block.Source != nil {
				file, err := toFile(block.Source)

				if err != nil {
					return nil, err
				}

				content = append(content, provider.FileContent(file))
			}

		case "tool_use":
			// Tool use in assistant message (for multi-turn conversations)
			args, err := toJSONString(block.Input)

			if err != nil {
				return nil, err
			}

			content = append(content, provider.ToolCallContent(provider.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: args,
			}))

		case "tool_result":
			// Tool result in user message
			result, err := toToolResultContent(block.Content)

			if err != nil {
				return nil, err
			}

			content = append(content, provider.ToolResultContent(provider.ToolResult{
				ID:   block.ToolUseID,
				Data: result,
			}))

		case "compaction":
			if compactionContent, ok := block.Content.(string); ok {
				content = append(content, provider.CompactionContent(provider.Compaction{
					Signature: compactionContent,
				}))
			}
		}
	}

	return &provider.Message{
		Role:    role,
		Content: content,
	}, nil
}

func toFile(source *ImageSource) (*provider.File, error) {
	if source == nil {
		return nil, nil
	}

	file := &provider.File{
		ContentType: source.MediaType,
	}

	switch source.Type {
	case "base64":
		data, err := base64.StdEncoding.DecodeString(source.Data)

		if err != nil {
			return nil, err
		}

		file.Content = data

	case "url":
		// For URL sources, we store the URL in the content
		// The provider should handle fetching if needed
		file.Content = []byte(source.URL)
	}

	return file, nil
}

func toToolResultContent(content any) (string, error) {
	if content == nil {
		return "", nil
	}

	switch v := content.(type) {
	case string:
		return v, nil

	case []any:
		// Array of content blocks - extract text
		var texts []string

		for _, item := range v {
			data, err := json.Marshal(item)

			if err != nil {
				return "", err
			}

			var block ContentBlockParam

			if err := json.Unmarshal(data, &block); err != nil {
				return "", err
			}

			if block.Type == "text" {
				texts = append(texts, block.Text)
			}
		}
		return strings.Join(texts, "\n"), nil

	default:
		data, err := json.Marshal(v)

		if err != nil {
			return "", err
		}

		return string(data), nil
	}
}

func toJSONString(v any) (string, error) {
	if v == nil {
		return "{}", nil
	}

	if s, ok := v.(string); ok {
		return s, nil
	}

	data, err := json.Marshal(v)

	if err != nil {
		return "", err
	}

	return string(data), nil
}

func toTools(tools []ToolParam) []provider.Tool {
	var result []provider.Tool

	for _, t := range tools {
		// Skip special tools — handled separately via options
		if strings.HasPrefix(t.Type, "text_editor") || strings.HasPrefix(t.Type, "computer") {
			continue
		}

		result = append(result, provider.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  tool.NormalizeSchema(t.InputSchema),
		})
	}

	return result
}

func toTextEditorToolOptions(tools []ToolParam) *provider.TextEditorOptions {
	for _, t := range tools {
		if strings.HasPrefix(t.Type, "text_editor") {
			return &provider.TextEditorOptions{}
		}
	}
	return nil
}

func toComputerUseToolOptions(tools []ToolParam) *provider.ComputerOptions {
	for _, t := range tools {
		if strings.HasPrefix(t.Type, "computer") {
			return &provider.ComputerOptions{
				DisplayWidth:  1024,
				DisplayHeight: 768,
			}
		}
	}
	return nil
}

func toContentBlocks(content []provider.Content) []ContentBlock {
	var result []ContentBlock

	for _, c := range content {
		if c.Reasoning != nil && (c.Reasoning.Text != "" || c.Reasoning.Signature != "") {
			result = append(result, ContentBlock{
				Type:      "thinking",
				Thinking:  c.Reasoning.Text,
				Signature: c.Reasoning.Signature,
			})
		}

		if c.Compaction != nil && c.Compaction.Signature != "" {
			result = append(result, ContentBlock{
				Type:    "compaction",
				Content: c.Compaction.Signature,
			})
		}

		if c.Text != "" {
			result = append(result, ContentBlock{
				Type: "text",
				Text: &c.Text,
			})
		}

		if c.ToolCall != nil {
			name := c.ToolCall.Name
			var input any

			if name == "apply_patch" {
				// Cross-provider: convert apply_patch args to text_editor input
				input = applyPatchArgsToTextEditorInput(c.ToolCall.Arguments)
				name = "str_replace_based_edit_tool"
			} else {
				if c.ToolCall.Arguments != "" {
					json.Unmarshal([]byte(c.ToolCall.Arguments), &input)
				}
			}

			if input == nil {
				input = map[string]any{}
			}

			result = append(result, ContentBlock{
				Type: "tool_use",

				ID:    c.ToolCall.ID,
				Name:  name,
				Input: input,

				Caller: &BlockCaller{Type: "direct"},
			})
		}
	}

	return result
}

// applyPatchArgsToTextEditorInput converts apply_patch JSON args to text_editor input format.
func applyPatchArgsToTextEditorInput(args string) map[string]any {
	var op struct {
		Type string `json:"type"`
		Path string `json:"path"`
		Diff string `json:"diff"`
	}

	json.Unmarshal([]byte(args), &op)

	switch op.Type {
	case "create_file":
		return map[string]any{
			"command":   "create",
			"path":      op.Path,
			"file_text": parseDiffAdded(op.Diff),
		}
	case "update_file":
		old, new_ := parseDiffOldNew(op.Diff)
		return map[string]any{
			"command": "str_replace",
			"path":    op.Path,
			"old_str": old,
			"new_str": new_,
		}
	default:
		return map[string]any{
			"command": "view",
			"path":    op.Path,
		}
	}
}

func parseDiffAdded(diff string) string {
	var lines []string
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+") {
			lines = append(lines, line[1:])
		}
	}
	return strings.Join(lines, "\n")
}

func parseDiffOldNew(diff string) (string, string) {
	var oldLines, newLines []string
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "-") {
			oldLines = append(oldLines, line[1:])
		} else if strings.HasPrefix(line, "+") {
			newLines = append(newLines, line[1:])
		}
	}
	return strings.Join(oldLines, "\n"), strings.Join(newLines, "\n")
}

func toStopReason(status provider.CompletionStatus, content []provider.Content) StopReason {
	if status == provider.CompletionStatusIncomplete {
		return StopReasonMaxTokens
	}

	for _, c := range content {
		if c.ToolCall != nil {
			return StopReasonToolUse
		}
	}

	return StopReasonEndTurn
}

func generateMessageID() string {
	return fmt.Sprintf("msg_%s", generateID(24))
}

func generateToolUseID() string {
	return fmt.Sprintf("toolu_%s", generateID(24))
}

func generateID(length int) string {
	bytes := make([]byte, length)
	rand.Read(bytes)

	return hex.EncodeToString(bytes)[:length]
}
