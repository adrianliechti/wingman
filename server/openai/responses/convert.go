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

func toToolOptions(v *ToolChoice) *provider.ToolOptions {
	if v == nil {
		return nil
	}

	choice := provider.ToolChoiceAuto

	switch v.Mode {
	case ToolChoiceModeNone:
		choice = provider.ToolChoiceNone
	case ToolChoiceModeRequired:
		choice = provider.ToolChoiceAny
	}

	var allowed []string

	for _, t := range v.AllowedTools {
		if t.Type == string(ToolTypeFunction) && t.Name != "" {
			allowed = append(allowed, t.Name)
		}
	}

	return &provider.ToolOptions{Choice: choice, Allowed: allowed}
}

func toMessages(items []InputItem, instructions string) ([]provider.Message, error) {
	result := make([]provider.Message, 0)

	if instructions != "" {
		result = append(result, provider.Message{
			Role:    provider.MessageRoleSystem,
			Content: []provider.Content{provider.TextContent(instructions)},
		})
	}

	// Pending buffers to accumulate and merge consecutive same-type items.
	// Consecutive function_call items map to one assistant message with multiple tool calls (parallel tool use).
	// Consecutive function_call_output items map to one user message with multiple tool results.
	// Reasoning items are merged into the following assistant message or function calls.
	var pendingReasoning []provider.Content
	var pendingCalls []provider.Content
	var pendingResults []provider.Content

	flushCalls := func() {
		if len(pendingCalls) == 0 && len(pendingReasoning) == 0 {
			return
		}

		result = append(result, provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: append(pendingReasoning, pendingCalls...),
		})

		pendingReasoning = nil
		pendingCalls = nil
	}

	flushResults := func() {
		if len(pendingResults) == 0 {
			return
		}

		result = append(result, provider.Message{
			Role:    provider.MessageRoleUser,
			Content: pendingResults,
		})

		pendingResults = nil
	}

	for _, item := range items {
		switch item.Type {
		case InputItemTypeMessage:
			if item.InputMessage == nil {
				continue
			}

			m := item.InputMessage

			content, err := toInputContent(m.Content)
			if err != nil {
				return nil, err
			}

			if m.Role == MessageRoleAssistant {
				flushResults()

				content = append(pendingReasoning, content...)
				pendingReasoning = nil

				if len(content) > 0 {
					result = append(result, provider.Message{
						Role:    provider.MessageRoleAssistant,
						Content: content,
					})
				}
			} else {
				flushCalls()
				flushResults()

				if len(content) > 0 {
					result = append(result, provider.Message{
						Role:    toMessageRole(m.Role),
						Content: content,
					})
				}
			}

		case InputItemTypeReasoning:
			if item.InputReasoning == nil {
				continue
			}

			r := provider.Reasoning{
				ID:        item.InputReasoning.ID,
				Signature: item.InputReasoning.EncryptedContent,
			}

			for _, part := range item.InputReasoning.Summary {
				if part.Type == "summary_text" {
					r.Summary += part.Text
				}
			}

			if len(item.InputReasoning.Content) > 0 && string(item.InputReasoning.Content) != "null" {
				var parts []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}

				if err := json.Unmarshal(item.InputReasoning.Content, &parts); err == nil {
					for _, part := range parts {
						if part.Type == "reasoning_text" {
							r.Text += part.Text
						}
					}
				}
			}

			pendingReasoning = append(pendingReasoning, provider.ReasoningContent(r))

		case InputItemTypeFunctionCall:
			if item.InputFunctionCall == nil {
				continue
			}

			flushResults()

			call := item.InputFunctionCall

			pendingCalls = append(pendingCalls, provider.ToolCallContent(provider.ToolCall{
				ID:        call.CallID,
				Name:      call.Name,
				Arguments: call.Arguments,
			}))

		case InputItemTypeFunctionCallOutput:
			if item.InputFunctionCallOutput == nil {
				continue
			}

			flushCalls()

			output := item.InputFunctionCallOutput

			pendingResults = append(pendingResults, provider.ToolResultContent(provider.ToolResult{
				ID:   output.CallID,
				Data: output.Output,
			}))

		case InputItemTypeApplyPatchCall:
			if item.InputApplyPatchCall == nil {
				continue
			}

			flushResults()

			call := item.InputApplyPatchCall

			pendingCalls = append(pendingCalls, provider.ToolCallContent(provider.ToolCall{
				ID:        call.CallID,
				Name:      "apply_patch",
				Arguments: call.Patch,
			}))

		case InputItemTypeApplyPatchCallOutput:
			if item.InputApplyPatchCallOutput == nil {
				continue
			}

			flushCalls()

			output := item.InputApplyPatchCallOutput

			pendingResults = append(pendingResults, provider.ToolResultContent(provider.ToolResult{
				ID:   output.CallID,
				Data: output.Output,
			}))
		}
	}

	flushCalls()
	flushResults()

	return result, nil
}

func toTools(tools []Tool) []provider.Tool {
	var result []provider.Tool

	for _, t := range tools {
		if t.Type == ToolTypeApplyPatch {
			result = append(result, provider.Tool{
				Type: provider.ToolTypeTextEditor,
				Name: "apply_patch",
			})

			continue
		}

		if t.Type != ToolTypeFunction {
			continue
		}

		result = append(result, provider.Tool{
			Name:        t.Name,
			Description: t.Description,
			Strict:      t.Strict,
			Parameters:  tool.NormalizeSchema(t.Parameters),
		})
	}

	return result
}

func toInputContent(items []InputContent) ([]provider.Content, error) {
	var result []provider.Content

	for _, c := range items {
		switch c.Type {
		case InputContentText:
			result = append(result, provider.TextContent(c.Text))

		case InputContentImage:
			file, err := shared.ToFile(c.ImageURL)
			if err != nil {
				return nil, err
			}

			result = append(result, provider.FileContent(file))

		case InputContentFile:
			file := &provider.File{Name: c.Filename}

			if c.FileData != "" {
				data, err := base64.StdEncoding.DecodeString(c.FileData)
				if err != nil {
					return nil, err
				}

				if mimeType := mime.TypeByExtension(path.Ext(c.Filename)); mimeType != "" {
					file.ContentType = mimeType
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

			result = append(result, provider.FileContent(file))
		}
	}

	return result, nil
}

func toMessageRole(r MessageRole) provider.MessageRole {
	switch r {
	case MessageRoleSystem, MessageRoleDeveloper:
		return provider.MessageRoleSystem
	case MessageRoleUser:
		return provider.MessageRoleUser
	case MessageRoleAssistant:
		return provider.MessageRoleAssistant
	default:
		return ""
	}
}
