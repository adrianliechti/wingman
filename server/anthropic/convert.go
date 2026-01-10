package anthropic

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func toMessages(system string, messages []MessageParam) ([]provider.Message, error) {
	var result []provider.Message

	// Add system message if present
	if system != "" {
		result = append(result, provider.SystemMessage(system))
	}

	for _, m := range messages {
		msg, err := toMessage(m)
		if err != nil {
			return nil, err
		}
		result = append(result, *msg)
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
			resultContent, err := toToolResultContent(block.Content)
			if err != nil {
				return nil, err
			}
			content = append(content, provider.ToolResultContent(provider.ToolResult{
				ID:   block.ToolUseID,
				Data: resultContent,
			}))
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
		tool := provider.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		}
		result = append(result, tool)
	}

	return result
}

func toContentBlocks(content []provider.Content) []ContentBlock {
	var result []ContentBlock

	for _, c := range content {
		if c.Text != "" {
			result = append(result, ContentBlock{
				Type: "text",
				Text: c.Text,
			})
		}

		if c.ToolCall != nil {
			var input any
			if c.ToolCall.Arguments != "" {
				json.Unmarshal([]byte(c.ToolCall.Arguments), &input)
			}
			if input == nil {
				input = map[string]any{}
			}

			result = append(result, ContentBlock{
				Type:  "tool_use",
				ID:    c.ToolCall.ID,
				Name:  c.ToolCall.Name,
				Input: input,
			})
		}
	}

	return result
}

func toStopReason(content []provider.Content) StopReason {
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
