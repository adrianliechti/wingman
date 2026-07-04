package anthropic

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/computeruse"
	"github.com/adrianliechti/wingman/pkg/provider/tools/shell"
	"github.com/adrianliechti/wingman/pkg/provider/tools/texteditor"
	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/adrianliechti/wingman/server/openai/shared"
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
	blocks, err := parseContentBlocks(m.Content)

	if err != nil {
		return nil, err
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
				return nil, err
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
			parts, err := toToolResultParts(block.Content)

			if err != nil {
				return nil, err
			}

			content = append(content, provider.ToolResultContent(provider.ToolResult{
				ID:    block.ToolUseID,
				Parts: parts,
			}))

		case "compaction":
			compaction := provider.Compaction{
				Signature: block.EncryptedContent,
			}

			if compactionContent, ok := block.Content.(string); ok {
				compaction.Content = compactionContent
			}

			if compaction.Content != "" || compaction.Signature != "" {
				content = append(content, provider.CompactionContent(compaction))
			}

		case "server_tool_use":
			if marker := serverToolUseMarker(block); marker != "" {
				content = append(content, provider.TextContent(marker))
			}

		case "web_search_tool_result":
			if marker := webSearchResultMarker(block); marker != "" {
				content = append(content, provider.TextContent(marker))
			}

		case "web_fetch_tool_result":
			if marker := webFetchResultMarker(block); marker != "" {
				content = append(content, provider.TextContent(marker))
			}
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
		fetched, err := shared.ToFile(source.URL)

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
	}

	return file, nil
}

func toToolResultParts(content any) ([]provider.Part, error) {
	if content == nil {
		return nil, nil
	}

	switch v := content.(type) {
	case string:
		return []provider.Part{{Text: v}}, nil

	case []any:
		var parts []provider.Part

		for _, item := range v {
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
						return nil, err
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
					return nil, err
				}
				parts = append(parts, provider.Part{File: file})
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

func toContentBlocks(content []provider.Content, includeThinking bool) []ContentBlock {
	var result []ContentBlock

	for _, c := range content {
		if includeThinking && c.Reasoning != nil && (c.Reasoning.Text != "" || c.Reasoning.Summary != "" || c.Reasoning.Signature != "") {
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
			result = append(result, ContentBlock{
				Type:             "compaction",
				Content:          c.Compaction.Content,
				EncryptedContent: c.Compaction.Signature,
			})
		}

		if c.Text != "" {
			result = append(result, ContentBlock{
				Type: "text",
				Text: &c.Text,
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

				result = append(result, ContentBlock{
					Type:  "server_tool_use",
					ID:    c.ToolCall.ID,
					Name:  "tool_search_tool_regex",
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
	}

	return result
}

func toStopReason(completion *provider.Completion) StopReason {
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
