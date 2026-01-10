package gemini

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/tool"
)

func toMessages(systemInstruction *Content, contents []*Content) ([]provider.Message, error) {
	var result []provider.Message

	// Handle system instruction
	if systemInstruction != nil && len(systemInstruction.Parts) > 0 {
		var systemText string
		for _, part := range systemInstruction.Parts {
			if part.Text != "" {
				if systemText != "" {
					systemText += "\n"
				}
				systemText += part.Text
			}
		}
		if systemText != "" {
			result = append(result, provider.SystemMessage(systemText))
		}
	}

	// Handle contents
	for _, c := range contents {
		message, err := toMessage(*c)
		if err != nil {
			return nil, err
		}
		result = append(result, *message)
	}

	return result, nil
}

func toMessage(c Content) (*provider.Message, error) {
	var role provider.MessageRole

	switch c.Role {
	case "user":
		role = provider.MessageRoleUser
	case "model":
		role = provider.MessageRoleAssistant
	default:
		role = provider.MessageRoleUser
	}

	var content []provider.Content

	for _, part := range c.Parts {
		// Text content
		if part.Text != "" {
			content = append(content, provider.TextContent(part.Text))
		}

		// Inline data (images, etc.)
		if part.InlineData != nil {
			data, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
			if err != nil {
				return nil, err
			}

			content = append(content, provider.FileContent(&provider.File{
				Content:     data,
				ContentType: part.InlineData.MimeType,
			}))
		}

		// Function call (in model/assistant messages)
		if part.FunctionCall != nil {
			args, err := toJSONString(part.FunctionCall.Args)
			if err != nil {
				return nil, err
			}

			id := part.FunctionCall.ID
			if id == "" {
				id = generateFunctionCallID()
			}

			content = append(content, provider.ToolCallContent(provider.ToolCall{
				ID:        id,
				Name:      part.FunctionCall.Name,
				Arguments: args,
			}))
		}

		// Function response (in user messages)
		if part.FunctionResponse != nil {
			result, err := toJSONString(part.FunctionResponse.Response)
			if err != nil {
				return nil, err
			}

			content = append(content, provider.ToolResultContent(provider.ToolResult{
				ID:   part.FunctionResponse.ID,
				Data: result,
			}))
		}
	}

	return &provider.Message{
		Role:    role,
		Content: content,
	}, nil
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

func toTools(tools []*Tool) []provider.Tool {
	var result []provider.Tool

	for _, t := range tools {
		for _, fd := range t.FunctionDeclarations {
			// Handle parameters - could be a map or nil
			var params map[string]any
			if fd.Parameters != nil {
				if p, ok := fd.Parameters.(map[string]any); ok {
					params = p
				}
			}

			result = append(result, provider.Tool{
				Name:        fd.Name,
				Description: fd.Description,
				Parameters:  tool.NormalizeSchema(params),
			})
		}
	}

	return result
}

func toContent(content []provider.Content) *Content {
	if len(content) == 0 {
		return nil
	}

	var parts []*Part

	for _, c := range content {
		if c.Text != "" {
			parts = append(parts, &Part{
				Text: c.Text,
			})
		}

		if c.ToolCall != nil {
			var args map[string]any

			if c.ToolCall.Arguments != "" {
				json.Unmarshal([]byte(c.ToolCall.Arguments), &args)
			}

			if args == nil {
				args = map[string]any{}
			}

			// The ID from upstream may already be formatted with ::name
			// Use the ID as-is since the upstream provider formats it
			parts = append(parts, &Part{
				FunctionCall: &FunctionCall{
					ID:   c.ToolCall.ID,
					Name: c.ToolCall.Name,
					Args: args,
				},
			})
		}
	}

	if len(parts) == 0 {
		return nil
	}

	return &Content{
		Role:  "model",
		Parts: parts,
	}
}

func toFinishReason(content []provider.Content) FinishReason {
	for _, c := range content {
		if c.ToolCall != nil {
			return FinishReasonStop // Gemini uses STOP for function calls too
		}
	}

	return FinishReasonStop
}

func generateResponseID() string {
	return fmt.Sprintf("resp_%s", generateID(24))
}

func generateFunctionCallID() string {
	return fmt.Sprintf("call_%s", generateID(24))
}

func generateID(length int) string {
	bytes := make([]byte, length)
	rand.Read(bytes)

	return hex.EncodeToString(bytes)[:length]
}
