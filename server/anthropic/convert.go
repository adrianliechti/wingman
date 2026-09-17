package anthropic

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/pkg/provider/tools/computeruse"
	"github.com/adrianliechti/wingman/pkg/provider/tools/shell"
	"github.com/adrianliechti/wingman/pkg/provider/tools/texteditor"
	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/adrianliechti/wingman/server/files"
)

func toMessages(system string, messages []MessageParam) ([]provider.Message, error) {
	var result []provider.Message

	if system != "" {
		result = append(result, provider.SystemMessage(system))
	}

	for i, m := range messages {
		message, err := toMessage(i, m)

		if err != nil {
			return nil, err
		}

		result = append(result, *message)
	}

	return result, nil
}

func toMessage(index int, m MessageParam) (*provider.Message, error) {
	if m.ClearAt != "" && (m.Role != MessageRoleSystem || (m.ClearAt != "never" && m.ClearAt != "next_user_message")) {
		return nil, fmt.Errorf("messages.%d.clear_at: requires a system message and either never or next_user_message", index)
	}
	if m.ClearAt == "next_user_message" && m.OutputConfig != nil {
		return nil, fmt.Errorf("messages.%d.output_config: turn-scoped instructions support only text", index)
	}
	blocks, err := parseContentBlocks(m.Content)

	if err != nil {
		return nil, fmt.Errorf("messages.%d.content: %w", index, err)
	}

	var role provider.MessageRole

	switch m.Role {
	case MessageRoleSystem:
		role = provider.MessageRoleSystem

	case MessageRoleUser:
		role = provider.MessageRoleUser

	case MessageRoleAssistant:
		role = provider.MessageRoleAssistant

	default:
		return nil, fmt.Errorf(
			"messages.%d: Unexpected role %q. Allowed roles are \"system\", \"user\" or \"assistant\"",
			index, m.Role,
		)
	}

	var content []provider.Content
	if m.OutputConfig != nil {
		if m.Role != MessageRoleSystem || m.OutputConfig.Format != nil || len(m.OutputConfig.TaskBudget) > 0 || !validEffort(m.OutputConfig.Effort) {
			return nil, fmt.Errorf("messages.%d.output_config: requires a system message and a valid effort", index)
		}
		content = append(content, provider.ConfigurationUpdateContent(provider.ConfigurationUpdate{ReasoningEffort: provider.Effort(m.OutputConfig.Effort)}))
	}

	for j, block := range blocks {
		path := fmt.Sprintf("messages.%d.content.%d", index, j)
		if m.Role == MessageRoleSystem && block.Type != "text" {
			return nil, fmt.Errorf("%s: system messages support only text and output_config.effort", path)
		}
		if block.Caller != nil && block.Caller.Type != "direct" {
			return nil, fmt.Errorf("%s.caller: only direct tool calls are supported", path)
		}

		switch block.Type {
		case "text":
			if m.Role == MessageRoleSystem {
				scope := provider.InstructionScopeConversation
				if m.ClearAt == "next_user_message" {
					scope = provider.InstructionScopeTurn
				}
				content = append(content, provider.InstructionsContent(provider.Instructions{Text: block.Text, Scope: scope}))
			} else {
				content = append(content, provider.TextContent(block.Text))
			}

		case "image":
			if block.Source != nil {
				file, err := toFile(block.Source)

				if err != nil {
					return nil, fmt.Errorf("%s.source: %w", path, err)
				}

				content = append(content, provider.FileContent(file))
			}

		case "document":
			if block.Source == nil {
				continue
			}

			// Plain-text documents inline as text — portable across providers
			// that don't have a dedicated document block (OpenAI, Bedrock, …).
			if block.Source.Type == "text" {
				if block.Source.Data != "" {
					content = append(content, provider.TextContent(block.Source.Data))
				}
				continue
			}

			file, err := toFile(block.Source)
			if err != nil {
				return nil, fmt.Errorf("%s.source: %w", path, err)
			}

			content = append(content, provider.FileContent(file))

		case "thinking":
			// Round-trip reasoning across turns: signature is the verifiable
			// blob Anthropic re-validates on the next call.
			id, signature := decodeSignature(block.Signature)

			r := provider.Reasoning{
				Text:      block.Thinking,
				Signature: signature,
			}

			// A wrapped id marks an OpenAI-backed item: its visible text was
			// the summary, and replayed content parts would be rejected.
			if id != "" {
				r.ID = id
				r.Text, r.Summary = "", block.Thinking
			}

			content = append(content, provider.ReasoningContent(r))

		case "redacted_thinking":
			// Encrypted thinking block — only the opaque `data` blob round-trips.
			content = append(content, provider.ReasoningContent(provider.Reasoning{
				Signature: block.Data,
				Redacted:  true,
			}))

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
			parts, err := toToolResultParts(path+".tool_result.content", block.Content)

			if err != nil {
				return nil, err
			}

			content = append(content, provider.ToolResultContent(provider.ToolResult{
				ID:      block.ToolUseID,
				IsError: block.IsError,
				Parts:   parts,
			}))

		case "compaction":
			compaction := provider.Compaction{
				Signature: block.EncryptedContent,
			}
			if block.Signature != "" {
				compaction.Signature = anthropic.WrapCompactionSignature(block.Signature)
			}

			if compactionContent, ok := block.Content.(string); ok {
				compaction.Content = compactionContent
			}

			if compaction.Content != "" || compaction.Signature != "" {
				content = append(content, provider.CompactionContent(compaction))
			}

		case "server_tool_use":
			if strings.HasPrefix(block.Name, "tool_search_tool_") {
				args, err := toJSONString(block.Input)
				if err != nil {
					return nil, err
				}
				content = append(content, provider.ToolCallContent(provider.ToolCall{ID: block.ID, Name: block.Name, Kind: provider.ToolKindToolSearch, Execution: "server", Arguments: args}))
				break
			}
			if marker := serverToolUseMarker(block); marker != "" {
				content = append(content, provider.TextContent(marker))
			}
		case "tool_search_tool_result":
			data, err := json.Marshal(block.Content)
			if err != nil {
				return nil, err
			}
			result, err := anthropic.ParseToolSearchResult(block.ToolUseID, data, nil)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			content = append(content, provider.ToolResultContent(result))

		case "web_search_tool_result":
			if marker := webSearchResultMarker(block); marker != "" {
				content = append(content, provider.TextContent(marker))
			}

		case "web_fetch_tool_result":
			if marker := webFetchResultMarker(block); marker != "" {
				content = append(content, provider.TextContent(marker))
			}

		default:
			return nil, fmt.Errorf(
				"%s: Input tag '%s' found using 'type' does not match any of the expected tags: 'compaction', 'document', 'image', 'redacted_thinking', 'server_tool_use', 'text', 'thinking', 'tool_result', 'tool_use', 'web_fetch_tool_result', 'web_search_tool_result'",
				path, block.Type,
			)
		}
	}

	return &provider.Message{
		Role:    role,
		Content: content,
	}, nil
}

func toFile(source *BlockSource) (*provider.File, error) {
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
		// No provider consumes raw URLs — fetch the content here so URL
		// sources work across all backends.
		fetched, err := files.FromURL(source.URL)

		if err != nil {
			return nil, err
		}

		file.Content = fetched.Content

		if file.ContentType == "" {
			file.ContentType = fetched.ContentType
		}

	case "text":
		// Plain-text document source — pass the bytes through.
		file.Content = []byte(source.Data)
		if file.ContentType == "" {
			file.ContentType = "text/plain"
		}

	default:
		// Files API references and custom-content documents cannot be
		// bridged to other backends.
		return nil, fmt.Errorf("source type '%s' is not supported; expected 'base64', 'url' or 'text'", source.Type)
	}

	return file, nil
}

func toToolResultParts(path string, content any) ([]provider.Part, error) {
	if content == nil {
		return nil, nil
	}

	switch v := content.(type) {
	case string:
		return []provider.Part{{Text: v}}, nil

	case []any:
		var parts []provider.Part

		for j, item := range v {
			data, err := json.Marshal(item)

			if err != nil {
				return nil, err
			}

			var block ContentBlockParam

			if err := json.Unmarshal(data, &block); err != nil {
				return nil, err
			}

			switch block.Type {
			case "text":
				if block.Text != "" {
					parts = append(parts, provider.Part{Text: block.Text})
				}

			case "image":
				if block.Source != nil {
					file, err := toFile(block.Source)
					if err != nil {
						return nil, fmt.Errorf("%s.%d.source: %w", path, j, err)
					}
					parts = append(parts, provider.Part{File: file})
				}

			case "document":
				if block.Source == nil {
					continue
				}
				// Plain-text documents inline as text (portable across providers
				// that don't have a dedicated document block).
				if block.Source.Type == "text" {
					if block.Source.Data != "" {
						parts = append(parts, provider.Part{Text: block.Source.Data})
					}
					continue
				}
				file, err := toFile(block.Source)
				if err != nil {
					return nil, fmt.Errorf("%s.%d.source: %w", path, j, err)
				}
				parts = append(parts, provider.Part{File: file})

			default:
				return nil, fmt.Errorf(
					"%s.%d: Input tag '%s' found using 'type' does not match any of the expected tags: 'document', 'image', 'text'",
					path, j, block.Type,
				)
			}
		}
		return parts, nil

	default:
		data, err := json.Marshal(v)

		if err != nil {
			return nil, err
		}

		return []provider.Part{{Text: string(data)}}, nil
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

func toTools(tools []ToolParam) ([]provider.Tool, error) {
	var result []provider.Tool

	for i, t := range tools {
		switch {
		case strings.Contains(t.Type, "_toolset_"):
			// Tool collections require shared member identities and result types.
			// Other backends cannot emulate this protocol as a legacy tool.
			return nil, fmt.Errorf(
				"tools.%d: Tool type '%s' is not supported; use the single-tool 'computer_*', 'bash_*' or 'text_editor_*' types",
				i, t.Type,
			)

		case strings.HasPrefix(t.Type, "text_editor"):
			result = append(result, provider.Tool{
				Name:          texteditor.NameTextEditor,
				Kind:          provider.ToolKindTextEditor,
				MaxCharacters: t.MaxCharacters,
			})

		case strings.HasPrefix(t.Type, "computer"):
			result = append(result, provider.Tool{
				Name:    computeruse.Name,
				Kind:    provider.ToolKindComputer,
				Dialect: computeruse.DialectAnthropic,
				Display: &provider.Display{
					Width:  t.DisplayWidthPx,
					Height: t.DisplayHeightPx,
				},
			})

		case strings.HasPrefix(t.Type, "bash"):
			result = append(result, provider.Tool{
				Name: shell.NameBash,
				Kind: provider.ToolKindShell,
			})

		case strings.HasPrefix(t.Type, "tool_search_tool"):
			result = append(result, provider.Tool{
				Name:      t.Name,
				Kind:      provider.ToolKindToolSearch,
				Execution: "server",
			})

		case t.Type == "" || t.Type == "custom":
			converted := provider.Tool{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  tool.NormalizeSchema(t.InputSchema),
				Strict:      t.Strict,
			}

			if t.DeferLoading {
				deferred := true
				converted.Deferred = &deferred
			}

			result = append(result, converted)

		default:
			return nil, fmt.Errorf(
				"tools.%d: Input tag '%s' found using 'type' does not match any of the expected tags: 'custom', 'text_editor_*', 'computer_*', 'bash_*', 'tool_search_tool_*'",
				i, t.Type,
			)
		}
	}

	return result, nil
}

func toContentBlocks(content []provider.Content) []ContentBlock {
	result := make([]ContentBlock, 0, len(content))

	for _, c := range content {
		if c.Reasoning != nil && (c.Reasoning.Text != "" || c.Reasoning.Summary != "" || c.Reasoning.Signature != "") {
			if c.Reasoning.Redacted {
				result = append(result, ContentBlock{
					Type: "redacted_thinking",
					Data: c.Reasoning.Signature,
				})
			} else {
				thinking := c.Reasoning.Text
				if thinking == "" {
					thinking = c.Reasoning.Summary
				}

				result = append(result, ContentBlock{
					Type:      "thinking",
					Thinking:  thinking,
					Signature: encodeSignature(c.Reasoning.ID, c.Reasoning.Signature),
				})
			}
		}

		if c.Compaction != nil && (c.Compaction.Content != "" || c.Compaction.Signature != "") {
			block := ContentBlock{
				Type:             "compaction",
				Content:          c.Compaction.Content,
				EncryptedContent: c.Compaction.Signature,
			}
			if signature, signed := anthropic.UnwrapCompactionSignature(c.Compaction.Signature); signed {
				block.Signature, block.EncryptedContent = signature, ""
			}
			result = append(result, block)
		}

		if c.Text != "" {
			result = append(result, ContentBlock{
				Type: "text",
				Text: &c.Text,
			})
		}

		// The Messages API has no refusal block; the refusal text is the only
		// explanation the client gets, so surface it as text.
		if c.Refusal != "" {
			result = append(result, ContentBlock{
				Type: "text",
				Text: &c.Refusal,
			})
		}

		if c.ToolCall != nil {
			if c.ToolCall.Kind == provider.ToolKindToolSearch && c.ToolCall.Execution != "client" {
				// server-executed search — informational, no client response expected
				var input any
				if c.ToolCall.Arguments != "" {
					json.Unmarshal([]byte(c.ToolCall.Arguments), &input)
				}
				if input == nil {
					input = map[string]any{}
				}

				name := c.ToolCall.Name
				if !strings.HasPrefix(name, "tool_search_tool_") {
					name = "tool_search_tool_regex"
				}
				result = append(result, ContentBlock{
					Type:  "server_tool_use",
					ID:    c.ToolCall.ID,
					Name:  name,
					Input: input,
				})
				continue
			}

			name := c.ToolCall.Name
			var input any

			if name == texteditor.NameApplyPatch {
				// Cross-dialect fallback (e.g. mixed histories): convert
				// apply_patch args to text_editor input
				input = texteditor.ParseOperation(c.ToolCall.Arguments).Input().Map()
				name = texteditor.NameTextEditor
			} else if name == computeruse.Name && c.ToolCall.Kind == provider.ToolKindComputer {
				// Cross-dialect fallback: degrade OpenAI batched actions to a
				// single Anthropic action
				input = computeruse.AnthropicInput(c.ToolCall.Arguments)
			} else if (name == shell.NameShell || name == shell.NameLocalShell) && c.ToolCall.Kind == provider.ToolKindShell {
				// Cross-dialect fallback: render OpenAI shell actions as a
				// bash command
				input = shell.BashInput(c.ToolCall.Arguments)
				name = shell.NameBash
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
		if c.ToolResult != nil && c.ToolResult.Kind == provider.ToolKindToolSearch && c.ToolResult.Execution != "client" {
			result = append(result, ContentBlock{Type: "tool_search_tool_result", ToolUseID: c.ToolResult.ID, Content: anthropic.ToolSearchResultContent(*c.ToolResult)})
		}
	}

	return result
}

func toStopReason(completion *provider.Completion) StopReason {
	switch completion.StopReason {
	case provider.StopReasonEndTurn:
		return StopReasonEndTurn
	case provider.StopReasonMaxTokens:
		return StopReasonMaxTokens
	case provider.StopReasonStopSequence:
		return StopReasonStopSequence
	case provider.StopReasonToolUse:
		return StopReasonToolUse
	case provider.StopReasonPauseTurn:
		return StopReasonPauseTurn
	case provider.StopReasonCompaction:
		return StopReasonCompaction
	case provider.StopReasonRefusal:
		return StopReasonRefusal
	case provider.StopReasonContextExceeded:
		return StopReasonModelContextWindowExceeded
	}

	switch completion.Status {
	case provider.CompletionStatusIncomplete:
		return StopReasonMaxTokens
	case provider.CompletionStatusRefused:
		return StopReasonRefusal
	}

	if completion.Message != nil {
		for _, c := range completion.Message.Content {
			if c.ToolCall != nil {
				return StopReasonToolUse
			}
		}
	}

	if completion.StopSequence != "" {
		return StopReasonStopSequence
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

func serverToolUseMarker(block ContentBlockParam) string {
	input, _ := block.Input.(map[string]any)
	if input == nil {
		if data, err := json.Marshal(block.Input); err == nil {
			return fmt.Sprintf("[%s: %s]", block.Name, string(data))
		}
		return fmt.Sprintf("[%s]", block.Name)
	}

	if q, ok := input["query"].(string); ok && q != "" {
		return fmt.Sprintf("[%s: %q]", block.Name, q)
	}
	if u, ok := input["url"].(string); ok && u != "" {
		return fmt.Sprintf("[%s: %s]", block.Name, u)
	}

	data, _ := json.Marshal(input)
	return fmt.Sprintf("[%s: %s]", block.Name, string(data))
}

func webSearchResultMarker(block ContentBlockParam) string {
	items, _ := block.Content.([]any)
	if len(items) == 0 {
		return "[web_search_result: empty]"
	}

	var parts []string
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		title, _ := item["title"].(string)
		url, _ := item["url"].(string)
		if title == "" {
			title = url
		}
		if url != "" {
			parts = append(parts, fmt.Sprintf("%s (%s)", title, url))
		}
	}
	if len(parts) == 0 {
		return "[web_search_result: empty]"
	}
	return "[web_search_result: " + strings.Join(parts, "; ") + "]"
}

func webFetchResultMarker(block ContentBlockParam) string {
	item, _ := block.Content.(map[string]any)
	if item == nil {
		return "[web_fetch_result]"
	}

	url, _ := item["url"].(string)
	if url == "" {
		return "[web_fetch_result]"
	}
	return "[web_fetch_result: " + url + "]"
}
