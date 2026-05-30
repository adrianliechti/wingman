package chat

import (
	"encoding/base64"
	"fmt"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/adrianliechti/wingman/server/openai/shared"

	"github.com/google/uuid"
)

func streamUsage(req ChatCompletionRequest) bool {
	if req.StreamOptions == nil {
		return false
	}

	if req.StreamOptions.IncludeUsage == nil {
		return false
	}

	return *req.StreamOptions.IncludeUsage
}

func toToolOptions(v *ToolChoice) *provider.ToolOptions {
	if v == nil {
		return nil
	}

	choice := provider.ToolChoiceAuto

	switch v.Mode {
	case ToolChoiceModeNone:
		choice = provider.ToolChoiceNone
	case ToolChoiceModeAuto:
		choice = provider.ToolChoiceAuto
	case ToolChoiceModeRequired:
		choice = provider.ToolChoiceAny
	}

	if len(v.AllowedTools) == 0 {
		return &provider.ToolOptions{Choice: choice}
	}

	var allowed []string

	for _, t := range v.AllowedTools {
		if t.Function != nil && t.Function.Name != "" {
			allowed = append(allowed, t.Function.Name)
		}
	}

	return &provider.ToolOptions{
		Choice:  choice,
		Allowed: allowed,
	}
}

func toMessages(s []ChatCompletionMessage) ([]provider.Message, error) {
	result := make([]provider.Message, 0)

	for i, m := range s {
		role := toMessageRole(m.Role)

		if role == "" {
			return nil, &shared.InvalidValueError{
				Param:   fmt.Sprintf("messages[%d].role", i),
				Message: fmt.Sprintf("Invalid value: '%s'. Supported values are: 'system', 'developer', 'user', 'tool', 'assistant'.", m.Role),
			}
		}

		var content []provider.Content

		if m.ToolCallID != "" {
			// Tool messages: chat spec allows either a string Content or an
			// array of text Contents. Both forms collapse into ToolResult.Parts.
			result := provider.ToolResult{ID: m.ToolCallID}

			if m.Content != nil {
				result.Parts = append(result.Parts, provider.Part{Text: *m.Content})
			}

			for _, c := range m.Contents {
				if c.Type == MessageContentTypeText && c.Text != "" {
					result.Parts = append(result.Parts, provider.Part{Text: c.Text})
				}
			}

			content = append(content, provider.ToolResultContent(result))
		} else {
			if len(m.Contents) == 0 && m.Content != nil {
				content = append(content, provider.TextContent(*m.Content))
			}

			for _, c := range m.Contents {
				if c.Type == MessageContentTypeText {
					content = append(content, provider.TextContent(c.Text))
				}

				if c.Type == MessageContentTypeFile && c.File != nil {
					file, err := shared.ToFile(c.File.Data)

					if err != nil {
						return nil, err
					}

					if c.File.Name != "" {
						file.Name = c.File.Name
					}

					content = append(content, provider.FileContent(file))
				}

				if c.Type == MessageContentTypeImage && c.Image != nil {
					file, err := shared.ToFile(c.Image.URL)

					if err != nil {
						return nil, err
					}

					content = append(content, provider.FileContent(file))
				}

				if c.Type == MessageContentTypeAudio && c.Audio != nil {
					data, err := base64.StdEncoding.DecodeString(c.Audio.Data)

					if err != nil {
						return nil, err
					}

					file := &provider.File{
						Content: data,
					}

					if c.Audio.Format != "" {
						file.Name = uuid.NewString() + "." + c.Audio.Format

						file.ContentType = "audio/" + c.Audio.Format
						if c.Audio.Format == "mp3" {
							file.ContentType = "audio/mpeg"
						}
					}

					content = append(content, provider.FileContent(file))
				}
			}
		}

		for _, c := range m.ToolCalls {
			if c.Type == ToolTypeFunction && c.Function != nil {
				call := provider.ToolCall{
					ID: c.ID,

					Name:      c.Function.Name,
					Arguments: c.Function.Arguments,
				}

				content = append(content, provider.ToolCallContent(call))
			}
		}

		result = append(result, provider.Message{
			Role:    role,
			Content: content,
		})
	}

	return result, nil
}

func toMessageRole(r MessageRole) provider.MessageRole {
	switch r {
	case MessageRoleSystem, MessageRoleDeveloper:
		return provider.MessageRoleSystem

	case MessageRoleUser, MessageRoleTool:
		return provider.MessageRoleUser

	case MessageRoleAssistant:
		return provider.MessageRoleAssistant

	default:
		return ""
	}
}

func toTools(tools []Tool) ([]provider.Tool, error) {
	var result []provider.Tool

	for i, t := range tools {
		if t.Type != ToolTypeFunction {
			return nil, &shared.InvalidValueError{
				Param:   fmt.Sprintf("tools[%d].type", i),
				Message: fmt.Sprintf("Invalid value: '%s'. Supported values are: 'function'.", t.Type),
			}
		}

		if t.ToolFunction == nil {
			continue
		}

		result = append(result, provider.Tool{
			Name:        t.ToolFunction.Name,
			Description: t.ToolFunction.Description,

			Strict: t.ToolFunction.Strict,

			Parameters: tool.NormalizeSchema(t.ToolFunction.Parameters),
		})
	}

	return result, nil
}

func oaiToolCalls(content []provider.Content) []ToolCall {
	result := make([]ToolCall, 0)

	for _, c := range content {
		if c.ToolCall == nil {
			continue
		}

		call := ToolCall{
			ID: c.ToolCall.ID,

			Type: ToolTypeFunction,

			Function: &FunctionCall{
				Name:      c.ToolCall.Name,
				Arguments: c.ToolCall.Arguments,
			},
		}

		result = append(result, call)
	}

	return result
}
