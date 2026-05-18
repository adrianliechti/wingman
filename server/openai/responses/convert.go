package responses

import (
	"encoding/base64"
	"encoding/json"
	"mime"
	"path"
	"strings"

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

		case InputItemTypeCompaction:
			if item.InputCompaction == nil {
				continue
			}

			flushCalls()
			flushResults()

			if item.InputCompaction.EncryptedContent != "" {
				result = append(result, provider.Message{
					Role: provider.MessageRoleAssistant,
					Content: []provider.Content{
						provider.CompactionContent(provider.Compaction{
							ID:        item.InputCompaction.ID,
							Signature: item.InputCompaction.EncryptedContent,
						}),
					},
				})
			}

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

			parts, err := toParts(output.Output)
			if err != nil {
				return nil, err
			}

			pendingResults = append(pendingResults, provider.ToolResultContent(provider.ToolResult{
				ID:    output.CallID,
				Parts: parts,
			}))

		case InputItemTypeApplyPatchCall:
			if item.InputApplyPatchCall == nil {
				continue
			}

			flushResults()

			call := item.InputApplyPatchCall
			args, _ := json.Marshal(map[string]any{
				"type": call.Operation.Type,
				"path": call.Operation.Path,
				"diff": call.Operation.Diff,
			})

			pendingCalls = append(pendingCalls, provider.ToolCallContent(provider.ToolCall{
				ID:        call.CallID,
				Name:      "apply_patch",
				Arguments: string(args),
			}))

		case InputItemTypeApplyPatchCallOutput:
			if item.InputApplyPatchCallOutput == nil {
				continue
			}

			flushCalls()

			output := item.InputApplyPatchCallOutput

			parts, err := toParts(output.Output)
			if err != nil {
				return nil, err
			}

			pendingResults = append(pendingResults, provider.ToolResultContent(provider.ToolResult{
				ID:    output.CallID,
				Parts: parts,
			}))

		case InputItemTypeComputerCall:
			if item.InputComputerCall == nil {
				continue
			}

			flushResults()

			call := item.InputComputerCall
			args, _ := json.Marshal(map[string]any{
				"call_id": call.CallID,
				"actions": call.Actions,
			})

			pendingCalls = append(pendingCalls, provider.ToolCallContent(provider.ToolCall{
				ID:        call.CallID,
				Name:      "computer",
				Arguments: string(args),
			}))

		case InputItemTypeComputerCallOutput:
			if item.InputComputerCallOutput == nil {
				continue
			}

			flushCalls()

			output := item.InputComputerCallOutput
			parts, err := computerOutputParts(output.Output)
			if err != nil {
				return nil, err
			}

			pendingResults = append(pendingResults, provider.ToolResultContent(provider.ToolResult{
				ID:    output.CallID,
				Parts: parts,
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

func toTextEditorToolOptions(tools []Tool) *provider.TextEditorOptions {
	for _, t := range tools {
		if t.Type == ToolTypeApplyPatch {
			return &provider.TextEditorOptions{}
		}
	}
	return nil
}

func toComputerUseToolOptions(tools []Tool) *provider.ComputerOptions {
	for _, t := range tools {
		if t.Type == ToolTypeComputer {
			return &provider.ComputerOptions{}
		}
	}
	return nil
}

func isComputerToolCall(call provider.ToolCall) bool {
	return call.Name == "computer"
}

func toolCallToComputerCall(call provider.ToolCall, status string) *ComputerCallItem {
	item := &ComputerCallItem{
		ID:     "cu_" + call.ID,
		CallID: call.ID,
		Status: status,
	}

	var args map[string]any
	json.Unmarshal([]byte(call.Arguments), &args)

	if actions, ok := args["actions"].([]any); ok {
		item.Actions = actions
	}

	return item
}


// isApplyPatchToolCall returns true if the tool call is an apply_patch or text_editor call.
func isApplyPatchToolCall(call provider.ToolCall) bool {
	return call.Name == "apply_patch" || call.Name == "str_replace_based_edit_tool"
}

// toolCallToApplyPatchCall converts a ToolCall (from apply_patch or str_replace_based_edit_tool)
// to an ApplyPatchCallItem for the OpenAI responses API.
func toolCallToApplyPatchCall(call provider.ToolCall, status string) *ApplyPatchCallItem {
	item := &ApplyPatchCallItem{
		ID:     "apc_" + call.ID,
		CallID: call.ID,
		Status: status,
	}

	if call.Name == "apply_patch" {
		// Native OpenAI format: arguments are {type, path, diff}
		var op ApplyPatchOperation
		json.Unmarshal([]byte(call.Arguments), &op)
		item.Operation = op
	} else if call.Name == "str_replace_based_edit_tool" {
		// Anthropic format: arguments are {command, path, file_text, old_str, new_str, ...}
		var args struct {
			Command  string `json:"command"`
			Path     string `json:"path"`
			FileText string `json:"file_text"`
			OldStr   string `json:"old_str"`
			NewStr   string `json:"new_str"`
		}
		json.Unmarshal([]byte(call.Arguments), &args)

		switch args.Command {
		case "create":
			item.Operation = ApplyPatchOperation{
				Type: "create_file",
				Path: args.Path,
				Diff: toAddDiff(args.FileText),
			}
		case "str_replace":
			item.Operation = ApplyPatchOperation{
				Type: "update_file",
				Path: args.Path,
				Diff: toReplaceDiff(args.OldStr, args.NewStr),
			}
		default:
			item.Operation = ApplyPatchOperation{
				Type: "update_file",
				Path: args.Path,
			}
		}
	}

	return item
}

func toAddDiff(content string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		b.WriteString("+" + line + "\n")
	}
	return b.String()
}

func toReplaceDiff(oldText, newText string) string {
	var b strings.Builder
	b.WriteString("@@\n")
	for _, line := range strings.Split(strings.TrimRight(oldText, "\n"), "\n") {
		b.WriteString("-" + line + "\n")
	}
	for _, line := range strings.Split(strings.TrimRight(newText, "\n"), "\n") {
		b.WriteString("+" + line + "\n")
	}
	return b.String()
}

// computerOutputParts maps a computer_call_output.output object to Parts.
// Per the OpenAI Responses spec the payload is a computer_screenshot with
// either an image_url (often a data URL) or a file_id. Falls back to a JSON
// text part for any other shape so callers can still inspect the payload.
func computerOutputParts(output any) ([]provider.Part, error) {
	data, err := json.Marshal(output)
	if err != nil {
		return nil, err
	}

	var screenshot struct {
		Type     string `json:"type"`
		ImageURL string `json:"image_url"`
		FileID   string `json:"file_id"`
	}
	if err := json.Unmarshal(data, &screenshot); err == nil && screenshot.Type == "computer_screenshot" {
		if screenshot.ImageURL != "" {
			file, err := shared.ToFile(screenshot.ImageURL)
			if err != nil {
				return nil, err
			}
			return []provider.Part{{File: file}}, nil
		}
		// file_id form — we have no fetcher here; pass it through as text
		// so the downstream provider can resolve it if it supports the ID.
		if screenshot.FileID != "" {
			return []provider.Part{{Text: string(data)}}, nil
		}
	}

	return []provider.Part{{Text: string(data)}}, nil
}

func toParts(items []InputContent) ([]provider.Part, error) {
	var result []provider.Part

	for _, c := range items {
		switch c.Type {
		case InputContentText, OutputContentText, "":
			if c.Text != "" {
				result = append(result, provider.Part{Text: c.Text})
			}

		case InputContentImage:
			file, err := shared.ToFile(c.ImageURL)
			if err != nil {
				return nil, err
			}

			result = append(result, provider.Part{File: file})

		case InputContentFile:
			file, err := fileFromInputContent(c)
			if err != nil {
				return nil, err
			}
			result = append(result, provider.Part{File: file})
		}
	}

	return result, nil
}

// fileFromInputContent decodes an input_file content part into provider.File.
// FileData accepts either raw base64 (mime inferred from filename) or a full
// data URL (mime parsed from the URL prefix). FileURL is handled via
// shared.ToFile which supports http/https + data URLs.
func fileFromInputContent(c InputContent) (*provider.File, error) {
	file := &provider.File{Name: c.Filename}

	if c.FileData != "" {
		if strings.HasPrefix(c.FileData, "data:") {
			f, err := shared.ToFile(c.FileData)
			if err != nil {
				return nil, err
			}
			if file.Name == "" {
				file.Name = f.Name
			}
			file.Content = f.Content
			file.ContentType = f.ContentType
		} else {
			data, err := base64.StdEncoding.DecodeString(c.FileData)
			if err != nil {
				return nil, err
			}
			if mimeType := mime.TypeByExtension(path.Ext(c.Filename)); mimeType != "" {
				file.ContentType = mimeType
			}
			file.Content = data
		}
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

	return file, nil
}

func toInputContent(items []InputContent) ([]provider.Content, error) {
	var result []provider.Content

	for _, c := range items {
		switch c.Type {
		case InputContentText, OutputContentText:
			result = append(result, provider.TextContent(c.Text))

		case InputContentImage:
			file, err := shared.ToFile(c.ImageURL)
			if err != nil {
				return nil, err
			}

			result = append(result, provider.FileContent(file))

		case InputContentFile:
			file, err := fileFromInputContent(c)
			if err != nil {
				return nil, err
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
