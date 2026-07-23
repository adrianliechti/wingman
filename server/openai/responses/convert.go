package responses

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"path"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/computeruse"
	"github.com/adrianliechti/wingman/pkg/provider/tools/shell"
	"github.com/adrianliechti/wingman/pkg/provider/tools/texteditor"
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

	var pendingReasoning []provider.Content
	var pendingCalls []provider.Content
	var pendingResults []provider.Content

	kindByCallID := make(map[string]provider.ToolKind)

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

	for i, item := range items {
		switch item.Type {
		case InputItemTypeMessage:
			if item.InputMessage == nil {
				continue
			}

			m := item.InputMessage

			role := toMessageRole(m.Role)
			if role == "" {
				return nil, &shared.InvalidValueError{
					Param:   fmt.Sprintf("input[%d].role", i),
					Message: fmt.Sprintf("Invalid value: '%s'. Supported values are: 'system', 'developer', 'user', 'assistant'.", m.Role),
				}
			}

			content, err := toInputContent(m.Content)
			if err != nil {
				return nil, err
			}

			if role == provider.MessageRoleAssistant {
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
						Role:    role,
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

			if r.Signature == "" && r.Summary == "" && r.Text == "" {
				continue
			}

			pendingReasoning = append(pendingReasoning, provider.ReasoningContent(r))

		case InputItemTypeCompaction:
			if item.InputCompaction == nil {
				continue
			}

			flushCalls()
			flushResults()

			if item.InputCompaction.Content != "" || item.InputCompaction.EncryptedContent != "" {
				result = append(result, provider.Message{
					Role: provider.MessageRoleAssistant,
					Content: []provider.Content{
						provider.CompactionContent(provider.Compaction{
							ID:        item.InputCompaction.ID,
							Content:   item.InputCompaction.Content,
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
				Namespace: call.Namespace,
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
				Kind:      provider.ToolKindTextEditor,
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
				Kind:  provider.ToolKindTextEditor,
				Parts: parts,
			}))

		case InputItemTypeCustomToolCall:
			if item.InputCustomToolCall == nil {
				continue
			}

			flushResults()

			call := item.InputCustomToolCall

			kind := provider.ToolKindCustom
			name := call.Name
			args := call.Input

			if call.Name == texteditor.NameApplyPatch {
				kind = provider.ToolKindTextEditor
				args = texteditor.ParseEnvelope(call.Input).Args()
			}

			kindByCallID[call.CallID] = kind
			pendingCalls = append(pendingCalls, provider.ToolCallContent(provider.ToolCall{
				ID:        call.CallID,
				Kind:      kind,
				Name:      name,
				Arguments: args,
			}))

		case InputItemTypeCustomToolCallOutput:
			if item.InputCustomToolCallOutput == nil {
				continue
			}

			flushCalls()

			output := item.InputCustomToolCallOutput

			parts, err := toParts(output.Output)
			if err != nil {
				return nil, err
			}

			kind := kindByCallID[output.CallID]
			if kind == "" {
				kind = provider.ToolKindCustom
			}
			pendingResults = append(pendingResults, provider.ToolResultContent(provider.ToolResult{
				ID:    output.CallID,
				Kind:  kind,
				Parts: parts,
			}))

		case InputItemTypeComputerCall:
			if item.InputComputerCall == nil {
				continue
			}

			flushResults()

			call := item.InputComputerCall

			pendingCalls = append(pendingCalls, provider.ToolCallContent(provider.ToolCall{
				ID:        call.CallID,
				Kind:      provider.ToolKindComputer,
				Name:      computeruse.Name,
				Arguments: toComputerCallArgs(call.CallID, call.Actions, call.PendingSafetyChecks),
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

			result := provider.ToolResult{
				ID:    output.CallID,
				Kind:  provider.ToolKindComputer,
				Parts: parts,
			}

			if len(output.AcknowledgedSafetyChecks) > 0 {
				result.Payload, _ = json.Marshal(output.AcknowledgedSafetyChecks)
			}

			pendingResults = append(pendingResults, provider.ToolResultContent(result))

		case InputItemTypeShellCall, InputItemTypeLocalShellCall:
			if item.InputShellCall == nil {
				continue
			}

			flushResults()

			call := item.InputShellCall

			name := shell.NameShell
			if item.Type == InputItemTypeLocalShellCall {
				name = shell.NameLocalShell
			}

			pendingCalls = append(pendingCalls, provider.ToolCallContent(provider.ToolCall{
				ID:        call.CallID,
				Kind:      provider.ToolKindShell,
				Name:      name,
				Arguments: string(call.Action),
			}))

		case InputItemTypeShellCallOutput, InputItemTypeLocalShellCallOutput:
			if item.InputShellCallOutput == nil {
				continue
			}

			flushCalls()

			output := item.InputShellCallOutput

			callID := output.CallID
			if callID == "" {
				// local_shell_call_output may carry the call id as `id`
				callID = output.ID
			}

			text := string(output.Output)

			var plain string
			if err := json.Unmarshal(output.Output, &plain); err == nil {
				text = plain
			}

			payload, _ := json.Marshal(map[string]any{
				"type":   string(item.Type),
				"output": json.RawMessage(output.Output),
			})

			pendingResults = append(pendingResults, provider.ToolResultContent(provider.ToolResult{
				ID:      callID,
				Kind:    provider.ToolKindShell,
				Parts:   []provider.Part{{Text: shell.OutputText(text)}},
				Payload: payload,
			}))

		case InputItemTypeToolSearchCall:
			if item.InputToolSearchCall == nil {
				continue
			}

			flushResults()

			call := item.InputToolSearchCall

			pendingCalls = append(pendingCalls, provider.ToolCallContent(provider.ToolCall{
				ID:        call.CallID,
				Kind:      provider.ToolKindToolSearch,
				Name:      "tool_search",
				Execution: call.Execution,
				Arguments: string(call.Arguments),
			}))

		case InputItemTypeToolSearchOutput:
			if item.InputToolSearchOutput == nil {
				continue
			}

			flushCalls()

			output := item.InputToolSearchOutput

			pendingResults = append(pendingResults, provider.ToolResultContent(provider.ToolResult{
				ID:        output.CallID,
				Kind:      provider.ToolKindToolSearch,
				Execution: output.Execution,
				Payload:   []byte(output.Tools),
			}))
		}
	}

	flushCalls()
	flushResults()

	return result, nil
}

func requestTools(tools []Tool, items []InputItem) []Tool {
	result := append([]Tool(nil), tools...)

	for _, item := range items {
		if item.Type != InputItemTypeAdditionalTools || item.InputAdditionalTools == nil {
			continue
		}

		// The provider abstraction has a request-wide tool list. Codex's
		// Responses Lite request puts additional_tools first, so promoting its
		// tools here preserves the effective availability for that request.
		// Hosted tools such as web_search cannot run on BYOK providers like
		// Azure, so omit those while retaining every portable tool Wingman can
		// forward.
		for _, additionalTool := range item.InputAdditionalTools.Tools {
			if supportedToolType(additionalTool.Type) {
				result = append(result, additionalTool)
			}
		}
	}

	return result
}

func supportedToolType(toolType ToolType) bool {
	switch toolType {
	case ToolTypeFunction,
		ToolTypeCustom,
		ToolTypeApplyPatch,
		ToolTypeComputer,
		ToolTypeShell,
		ToolTypeLocalShell,
		ToolTypeNamespace,
		ToolTypeToolSearch:
		return true
	default:
		return false
	}
}

func toTools(tools []Tool) ([]provider.Tool, error) {
	var result []provider.Tool

	for i, t := range tools {
		switch t.Type {
		case ToolTypeFunction:
			if t.Name == "" {
				continue
			}
			result = append(result, provider.Tool{
				Name:        t.Name,
				Description: t.Description,
				Strict:      t.Strict,
				Deferred:    t.DeferLoading,
				Parameters:  tool.NormalizeSchema(t.Parameters),
			})

		case ToolTypeApplyPatch:
			result = append(result, provider.Tool{
				Name: "apply_patch",
				Kind: provider.ToolKindTextEditor,
			})

		case ToolTypeCustom:
			if t.Name == "" {
				continue
			}
			kind := provider.ToolKindCustom
			if t.Name == "apply_patch" {
				kind = provider.ToolKindTextEditor
			}
			tool := provider.Tool{
				Name:        t.Name,
				Description: t.Description,
				Kind:        kind,
				Deferred:    t.DeferLoading,
			}
			if kind == provider.ToolKindCustom && t.Format != nil {
				tool.Format = &provider.ToolFormat{
					Type:       t.Format.Type,
					Syntax:     t.Format.Syntax,
					Definition: t.Format.Definition,
				}
			}
			result = append(result, tool)

		case ToolTypeComputer:
			result = append(result, provider.Tool{
				Name:    computeruse.Name,
				Kind:    provider.ToolKindComputer,
				Dialect: computeruse.DialectOpenAI,
				Display: &provider.Display{
					Width:       t.DisplayWidth,
					Height:      t.DisplayHeight,
					Environment: t.Environment,
				},
			})

		case ToolTypeShell:
			result = append(result, provider.Tool{
				Name: shell.NameShell,
				Kind: provider.ToolKindShell,
			})

		case ToolTypeLocalShell:
			result = append(result, provider.Tool{
				Name: shell.NameLocalShell,
				Kind: provider.ToolKindShell,
			})

		case ToolTypeToolSearch:
			search := provider.Tool{
				Kind:        provider.ToolKindToolSearch,
				Description: t.Description,
				Execution:   t.Execution,
			}

			// hosted tool_search takes no schema — only normalize what the
			// client provided, or the upstream rejects the fabricated one
			if len(t.Parameters) > 0 {
				search.Parameters = tool.NormalizeSchema(t.Parameters)
			}

			result = append(result, search)

		case ToolTypeNamespace:
			if t.Name == "" {
				continue
			}
			var children []provider.Tool
			for _, inner := range t.Tools {
				switch inner.Type {
				case ToolTypeFunction, "":
					if inner.Name == "" {
						continue
					}
					children = append(children, provider.Tool{
						Name:        inner.Name,
						Description: inner.Description,
						Strict:      inner.Strict,
						Deferred:    inner.DeferLoading,
						Parameters:  tool.NormalizeSchema(inner.Parameters),
					})
				case ToolTypeCustom:
					if inner.Name == "" {
						continue
					}
					custom := provider.Tool{
						Name:        inner.Name,
						Description: inner.Description,
						Kind:        provider.ToolKindCustom,
						Deferred:    inner.DeferLoading,
					}
					if inner.Format != nil {
						custom.Format = &provider.ToolFormat{
							Type:       inner.Format.Type,
							Syntax:     inner.Format.Syntax,
							Definition: inner.Format.Definition,
						}
					}
					children = append(children, custom)
				}
			}
			if len(children) == 0 {
				continue
			}
			result = append(result, provider.Tool{
				Name:        t.Name,
				Description: t.Description,
				Tools:       children,
			})

		case ToolTypeWebSearch:
			// web_search is hosted by OpenAI and is unavailable on BYOK
			// backends such as Azure. Codex advertises it even when using a
			// custom provider, so accept and omit it instead of rejecting the
			// entire request.
			continue

		default:
			return nil, &shared.InvalidValueError{
				Param:   fmt.Sprintf("tools[%d].type", i),
				Message: fmt.Sprintf("Invalid value: '%s'. Supported values are: 'function', 'custom', 'apply_patch', 'computer', 'shell', 'local_shell', 'namespace', 'tool_search'.", t.Type),
			}
		}
	}

	return result, nil
}

// outputKind picks the wire-format wrapper for a tool call by name, using
// the request's original tool definitions. Calls returning under the
// Anthropic str_replace_based_edit_tool name are mapped to whichever
// apply_patch flavor the client originally registered.
func outputKind(name string, tools []Tool) provider.ToolKind {
	const strReplaceAlias = "str_replace_based_edit_tool"
	applyPatchAlias := name == "apply_patch" || name == strReplaceAlias

	for _, t := range tools {
		switch t.Type {
		case ToolTypeApplyPatch:
			if applyPatchAlias {
				return provider.ToolKindTextEditor
			}
		case ToolTypeCustom:
			if t.Name == name || (t.Name == "apply_patch" && name == strReplaceAlias) {
				return provider.ToolKindCustom
			}
		case ToolTypeComputer:
			if name == "computer" {
				return provider.ToolKindComputer
			}
		case ToolTypeShell, ToolTypeLocalShell:
			if name == shell.NameShell || name == shell.NameLocalShell || name == shell.NameBash {
				return provider.ToolKindShell
			}
		case ToolTypeToolSearch:
			if name == "tool_search" {
				return provider.ToolKindToolSearch
			}
		case ToolTypeFunction:
			if t.Name == name {
				return provider.ToolKindFunction
			}
		case ToolTypeNamespace:
			for _, inner := range t.Tools {
				if inner.Name != name {
					continue
				}
				if inner.Type == ToolTypeCustom {
					return provider.ToolKindCustom
				}
				return provider.ToolKindFunction
			}
		}
	}

	return provider.ToolKindFunction
}

// isApplyPatchToolCall returns true if the tool call is an apply_patch or text_editor call.
func isApplyPatchToolCall(call provider.ToolCall) bool {
	return call.Name == texteditor.NameApplyPatch || call.Name == texteditor.NameTextEditor
}

// toolCallToApplyPatchCall converts a ToolCall (from apply_patch or str_replace_based_edit_tool)
// to an ApplyPatchCallItem for the OpenAI responses API.
func toolCallToApplyPatchCall(call provider.ToolCall, status string) *ApplyPatchCallItem {
	item := &ApplyPatchCallItem{
		ID:     "apc_" + call.ID,
		Type:   "apply_patch_call",
		CallID: call.ID,
		Status: status,
	}

	var op texteditor.Operation

	if call.Name == texteditor.NameTextEditor {
		// Cross-dialect fallback (e.g. mixed histories): convert text_editor
		// commands to the closest apply_patch operation
		op = texteditor.ParseInput(call.Arguments).Operation()
	} else {
		// Native OpenAI format: arguments are {type, path, diff}
		op = texteditor.ParseOperation(call.Arguments)
	}

	item.Operation = &ApplyPatchOperation{
		Type: op.Type,
		Path: op.Path,
		Diff: op.Diff,
	}

	return item
}

// toolCallToCustomToolCall wraps a ToolCall as a custom_tool_call item.
// apply_patch calls are re-encoded as the *** Begin Patch envelope Codex's
// grammar requires; other tools pass their arguments through as raw input.
func toolCallToCustomToolCall(call provider.ToolCall, status string) *CustomToolCallItem {
	if isApplyPatchToolCall(call) {
		op := toolCallToApplyPatchCall(call, status).Operation
		return &CustomToolCallItem{
			ID:     "ctc_" + call.ID,
			Type:   "custom_tool_call",
			CallID: call.ID,
			Status: status,
			Name:   texteditor.NameApplyPatch,
			Input:  texteditor.Operation{Type: op.Type, Path: op.Path, Diff: op.Diff}.Envelope(),
		}
	}

	return &CustomToolCallItem{
		ID:     "ctc_" + call.ID,
		Type:   "custom_tool_call",
		CallID: call.ID,
		Status: status,
		Name:   call.Name,
		Input:  call.Arguments,
	}
}

// toComputerCallArgs encodes a computer call in the canonical
// {call_id, actions, pending_safety_checks} form.
func toComputerCallArgs(callID string, actions []any, checks []SafetyCheck) string {
	c := computeruse.Call{CallID: callID}

	for _, a := range actions {
		if action, ok := a.(map[string]any); ok {
			c.Actions = append(c.Actions, action)
		}
	}

	for _, check := range checks {
		c.PendingSafetyChecks = append(c.PendingSafetyChecks, computeruse.SafetyCheck(check))
	}

	return c.Args()
}

// toolCallToComputerCall converts a computer ToolCall of either dialect to a
// computer_call item for the OpenAI responses API.
func toolCallToComputerCall(call provider.ToolCall, status string) *ComputerCallItem {
	item := &ComputerCallItem{
		ID:     "cu_" + call.ID,
		Type:   "computer_call",
		CallID: call.ID,
		Status: status,

		Actions:             []any{},
		PendingSafetyChecks: []SafetyCheck{},
	}

	c := computeruse.ParseCall(call.Arguments)

	for _, action := range c.Actions {
		item.Actions = append(item.Actions, action)
	}

	for _, check := range c.PendingSafetyChecks {
		item.PendingSafetyChecks = append(item.PendingSafetyChecks, SafetyCheck(check))
	}

	return item
}

// shellOutputType picks the shell_call or local_shell_call wire item type by
// the dialect the client originally registered.
func shellOutputType(tools []Tool) ResponseOutputType {
	for _, t := range tools {
		if t.Type == ToolTypeLocalShell {
			return ResponseOutputTypeLocalShellCall
		}
	}

	return ResponseOutputTypeShellCall
}

// shellLocalEnvironment is the environment for shell calls executed by the
// client, the only mode wingman supports.
var shellLocalEnvironment = json.RawMessage(`{"type":"local"}`)

// emptyShellAction is the placeholder action for a call whose commands are
// not yet known (output_item.added) or were lost to truncation.
func emptyShellAction(itemType ResponseOutputType) json.RawMessage {
	if itemType == ResponseOutputTypeLocalShellCall {
		return json.RawMessage(`{"type":"exec","command":[]}`)
	}

	return json.RawMessage(`{"commands":[]}`)
}

// toolCallToShellCall converts a shell ToolCall of any dialect to a
// shell_call or local_shell_call item for the OpenAI responses API.
func toolCallToShellCall(call provider.ToolCall, status string, itemType ResponseOutputType) *ShellCallItem {
	item := &ShellCallItem{
		ID:     "sh_" + call.ID,
		Type:   string(itemType),
		CallID: call.ID,
		Status: status,
	}

	var action map[string]any

	if itemType == ResponseOutputTypeLocalShellCall {
		action = shell.LocalShellAction(call.Arguments)
	} else {
		action = shell.ShellAction(call.Arguments)
		item.Environment = shellLocalEnvironment
	}

	if action == nil {
		item.Action = emptyShellAction(itemType)
	} else {
		item.Action, _ = json.Marshal(action)
	}

	return item
}

func toolCallToToolSearchCall(call provider.ToolCall, status string) *ToolSearchCallItem {
	item := &ToolSearchCallItem{
		ID:        "tsc_" + call.ID,
		Type:      "tool_search_call",
		CallID:    call.ID,
		Status:    status,
		Execution: call.Execution,
		Arguments: json.RawMessage("{}"),
	}
	if item.Execution == "" {
		item.Execution = "server"
	}
	if call.Arguments != "" {
		item.Arguments = json.RawMessage(call.Arguments)
	}
	return item
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

		case InputContentRefusal:
			result = append(result, provider.RefusalContent(c.Refusal))

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
