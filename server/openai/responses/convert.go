package responses

import (
	"encoding/base64"
	"encoding/json"
	"mime"
	"path"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/adrianliechti/wingman/server/openai/shared"
)

func toMessages(items []InputItem, instructions string) ([]provider.Message, error) {
	result := make([]provider.Message, 0)

	if instructions != "" {
		result = append(result, provider.Message{
			Role:    provider.MessageRoleSystem,
			Content: []provider.Content{provider.TextContent(instructions)},
		})
	}

	// Track pending tool calls to merge with their results
	var pendingToolCalls []provider.ToolCall

	for _, item := range items {
		switch item.Type {
		case InputItemTypeMessage:
			if item.InputMessage == nil {
				continue
			}

			m := item.InputMessage
			var content []provider.Content

			for _, c := range m.Content {
				if c.Type == InputContentText {
					content = append(content, provider.TextContent(c.Text))
				}

				if c.Type == InputContentImage {
					file, err := shared.ToFile(c.ImageURL)

					if err != nil {
						return nil, err
					}

					content = append(content, provider.FileContent(file))
				}

				if c.Type == InputContentFile {
					file := &provider.File{
						Name: c.Filename,
					}

					if c.FileData != "" {
						data, err := base64.StdEncoding.DecodeString(c.FileData)

						if err != nil {
							return nil, err
						}

						if mime := mime.TypeByExtension(path.Ext(c.Filename)); mime != "" {
							file.ContentType = mime
						}

						file.Content = data
					}

					if c.FileURL != "" {
						f, err := shared.ToFile(c.FileURL)

						if err != nil {
							return nil, err
						}

						if file.Name == "" {
							file.Name = f.Name
						}

						file.Content = f.Content
						file.ContentType = f.ContentType
					}

					content = append(content, provider.FileContent(file))
				}
			}

			if m.Role == MessageRoleAssistant && len(pendingToolCalls) > 0 {
				for _, call := range pendingToolCalls {
					content = append(content, provider.ToolCallContent(call))
				}

				pendingToolCalls = nil
			}

			if len(content) > 0 {
				result = append(result, provider.Message{
					Role:    toMessageRole(m.Role),
					Content: content,
				})
			}

		case InputItemTypeReasoning:
			if item.InputReasoning == nil {
				continue
			}

			reasoning := item.InputReasoning

			// Build summary text from summary parts
			var summaryText string
			for _, part := range reasoning.Summary {
				if part.Type == "summary_text" {
					summaryText += part.Text
				}
			}

			// Parse reasoning content if present
			var reasoningText string
			if len(reasoning.Content) > 0 && string(reasoning.Content) != "null" {
				// Content is an array of content parts with type and text
				var contentParts []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if err := json.Unmarshal(reasoning.Content, &contentParts); err == nil {
					for _, part := range contentParts {
						if part.Type == "reasoning_text" {
							reasoningText += part.Text
						}
					}
				}
			}

			result = append(result, provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{
					provider.ReasoningContent(provider.Reasoning{
						Text:      reasoningText,
						Summary:   summaryText,
						Signature: reasoning.EncryptedContent,
					}),
				},
			})

		case InputItemTypeFunctionCall:
			if item.InputFunctionCall == nil {
				continue
			}

			call := item.InputFunctionCall

			toolCall := provider.ToolCall{
				ID:        call.CallID,
				Name:      call.Name,
				Arguments: call.Arguments,
			}

			result = append(result, provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{
					provider.ToolCallContent(toolCall),
				},
			})

		case InputItemTypeFunctionCallOutput:
			if item.InputFunctionCallOutput == nil {
				continue
			}

			output := item.InputFunctionCallOutput

			result = append(result, provider.Message{
				Role: provider.MessageRoleUser,
				Content: []provider.Content{
					provider.ToolResultContent(provider.ToolResult{
						ID:   output.CallID,
						Data: output.Output,
					}),
				},
			})
		}
	}

	return result, nil
}

func toTools(tools []Tool) ([]provider.Tool, error) {
	if len(tools) == 0 {
		return nil, nil
	}

	result := make([]provider.Tool, 0, len(tools))

	for _, t := range tools {
		// Only support function tools for now
		// Custom tools (like apply_patch) require special handling by the model
		if t.Type == ToolTypeFunction {
			tool := provider.Tool{
				Name:        t.Name,
				Description: t.Description,
				Strict:      t.Strict,
				Parameters:  tool.NormalizeSchema(t.Parameters),
			}
			result = append(result, tool)
		}
		// Note: Custom tools with grammar format are passed through to the model
		// but may require special handling in the completer
	}

	return result, nil
}

func toMessageRole(r MessageRole) provider.MessageRole {
	switch r {
	case MessageRoleSystem:
		return provider.MessageRoleSystem

	case MessageRoleUser: // MessageRoleTool
		return provider.MessageRoleUser

	case MessageRoleAssistant:
		return provider.MessageRoleAssistant

	default:
		return ""
	}
}
