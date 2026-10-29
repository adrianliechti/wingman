package gemini

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/adrianliechti/wingman/server/files"
)

func toMessages(systemInstruction *Content, contents []*Content) ([]provider.Message, error) {
	var result []provider.Message

	// Handle system instruction
	if systemInstruction != nil && len(systemInstruction.Parts) > 0 {
		var systemText string
		for _, part := range systemInstruction.Parts {
			if part == nil {
				continue
			}
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

	// Pair function_response items with their matching function_call by
	// name+order so that wingman has consistent IDs on both sides even when
	// the wire request omits explicit ids. The Google completer's lookup of
	// the tool name (required by Gemini's FunctionResponse) is keyed on this
	// id, so call_id and response_id must match.
	pendingCallIDs := map[string][]string{}

	for _, c := range contents {
		if c == nil {
			continue
		}
		message, err := toMessage(*c, pendingCallIDs)
		if err != nil {
			return nil, err
		}
		result = append(result, *message)
	}

	return result, nil
}

func toMessage(c Content, pendingCallIDs map[string][]string) (*provider.Message, error) {
	var role provider.MessageRole

	hasFunctionResponse := false
	for _, part := range c.Parts {
		if part != nil && part.FunctionResponse != nil {
			hasFunctionResponse = true
			break
		}
	}

	switch {
	case hasFunctionResponse, c.Role == "", c.Role == "user":
		role = provider.MessageRoleUser
	case c.Role == "model":
		role = provider.MessageRoleAssistant
	default:
		return nil, fmt.Errorf("Please use a valid role: user, model.")
	}

	var content []provider.Content

	for _, part := range c.Parts {
		if part == nil {
			continue
		}
		sig := string(part.ThoughtSignature)
		// Native Gemini signatures are binary; Interactions expects base64.
		// Wingman's realm-tagged signatures already hold the encoded value.
		if realm, _ := provider.ParseSignature(sig); realm == "" {
			sig = google.EncodeThoughtSignature(part.ThoughtSignature)
		}

		if part.Thought {
			content = append(content, provider.ReasoningContent(provider.Reasoning{
				Content:   provider.TextParts(part.Text),
				Signature: sig,
			}))
			continue
		}

		// The signature precedes the part's content, including a function call:
		// Gemini requires it back on the call that opened the step.
		if sig != "" && part.FunctionResponse == nil {
			content = append(content, provider.ReasoningContent(provider.Reasoning{
				Signature: sig,
			}))
		}

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

		// File references: only fetchable URLs can be bridged to other
		// backends; Files API URIs are bound to the caller's Google project.
		if part.FileData != nil {
			uri := part.FileData.FileUri

			if !strings.HasPrefix(uri, "http://") && !strings.HasPrefix(uri, "https://") {
				return nil, fmt.Errorf("fileData.fileUri %q is not supported: only http(s) URLs can be fetched", uri)
			}

			file, err := files.FromURL(uri)
			if err != nil {
				return nil, err
			}

			if part.FileData.MimeType != "" {
				file.ContentType = part.FileData.MimeType
			}

			content = append(content, provider.FileContent(file))
		}

		// Function call (in model/assistant messages)
		if part.FunctionCall != nil {
			arguments := part.FunctionCall.Args
			if arguments == nil {
				arguments = map[string]any{}
			}
			args, err := toJSONString(arguments)
			if err != nil {
				return nil, err
			}

			id := part.FunctionCall.ID
			if id == "" {
				id = generateFunctionCallID()
			}

			name := part.FunctionCall.Name
			pendingCallIDs[name] = append(pendingCallIDs[name], id)

			content = append(content, provider.ToolCallContent(provider.ToolCall{
				ID:        id,
				Name:      name,
				Arguments: args,
			}))
		}

		// Function response (in user messages)
		if part.FunctionResponse != nil {
			var parts []provider.Part

			if len(part.FunctionResponse.Response) > 0 {
				result, err := toJSONString(part.FunctionResponse.Response)
				if err != nil {
					return nil, err
				}
				parts = append(parts, provider.Part{Text: result})
			}

			for _, p := range part.FunctionResponse.Parts {
				if p == nil {
					continue
				}
				if p.InlineData != nil {
					data, err := base64.StdEncoding.DecodeString(p.InlineData.Data)
					if err != nil {
						return nil, err
					}
					parts = append(parts, provider.Part{
						File: &provider.File{
							Name:        p.InlineData.DisplayName,
							Content:     data,
							ContentType: p.InlineData.MimeType,
						},
					})
				}
				if p.FileData != nil {
					uri := p.FileData.FileUri

					if !strings.HasPrefix(uri, "http://") && !strings.HasPrefix(uri, "https://") {
						return nil, fmt.Errorf("fileData.fileUri %q is not supported: only http(s) URLs can be fetched", uri)
					}

					file, err := files.FromURL(uri)
					if err != nil {
						return nil, err
					}

					if p.FileData.DisplayName != "" {
						file.Name = p.FileData.DisplayName
					}
					if p.FileData.MimeType != "" {
						file.ContentType = p.FileData.MimeType
					}

					parts = append(parts, provider.Part{File: file})
				}
			}

			id := part.FunctionResponse.ID
			if id == "" {
				// Match this response to the next unanswered call with the
				// same name so the pair shares a stable id downstream.
				name := part.FunctionResponse.Name
				if queue := pendingCallIDs[name]; len(queue) > 0 {
					id = queue[0]
					pendingCallIDs[name] = queue[1:]
				} else {
					id = generateFunctionCallID()
				}
			}

			content = append(content, provider.ToolResultContent(provider.ToolResult{
				ID:    id,
				Parts: parts,
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

func toTools(tools []*Tool, strict bool) []provider.Tool {
	var result []provider.Tool

	for _, t := range tools {
		if t == nil {
			continue
		}
		for _, fd := range t.FunctionDeclarations {
			if fd == nil {
				continue
			}
			// Handle parameters - could be from Parameters or ParametersJsonSchema (CLI format)
			var params map[string]any

			// Try Parameters first, then fall back to ParametersJsonSchema (Gemini CLI uses this)
			paramSource := fd.Parameters
			if paramSource == nil {
				paramSource = fd.ParametersJsonSchema
			}

			if paramSource != nil {
				if p, ok := paramSource.(map[string]any); ok {
					params = p
				}
			}

			tool := provider.Tool{
				Name:        fd.Name,
				Description: fd.Description,
				Parameters:  tool.NormalizeSchema(params),
			}

			if strict {
				tool.Strict = &strict
			}

			result = append(result, tool)
		}
	}

	return result
}

func toContent(content []provider.Content) *Content {
	if len(content) == 0 {
		return nil
	}

	var parts []*Part

	var pendingSig string

	for i, c := range content {
		if c.Reasoning != nil {
			beforeCall := i+1 < len(content) && content[i+1].ToolCall != nil
			text := c.Reasoning.Text()
			if text == "" {
				text = c.Reasoning.SummaryText()
			}

			if text == "" {
				if c.Reasoning.Signature != "" {
					// A replayed signature precedes the part it belongs to; a
					// terminal signature-only delta belongs to the preceding part.
					beforePart := beforeCall || i+1 < len(content) && (content[i+1].Text != "" || content[i+1].Refusal != "")
					if n := len(parts); !beforePart && n > 0 && len(parts[n-1].ThoughtSignature) == 0 && !parts[n-1].Thought {
						parts[n-1].ThoughtSignature = google.DecodeThoughtSignature(c.Reasoning.Signature)
					} else {
						pendingSig = c.Reasoning.Signature
					}
				}
				continue
			}

			part := &Part{
				Text:    text,
				Thought: true,
			}

			if c.Reasoning.Signature != "" {
				if beforeCall {
					pendingSig = c.Reasoning.Signature
				} else {
					part.ThoughtSignature = google.DecodeThoughtSignature(c.Reasoning.Signature)
				}
			}

			parts = append(parts, part)
		}

		if text := c.Text + c.Refusal; text != "" {
			part := &Part{
				Text: text,
			}
			if pendingSig != "" {
				part.ThoughtSignature = google.DecodeThoughtSignature(pendingSig)
				pendingSig = ""
			}
			parts = append(parts, part)
		}

		if c.ToolCall != nil {
			var args map[string]any

			if c.ToolCall.Arguments != "" {
				if err := decodeJSON(strings.NewReader(c.ToolCall.Arguments), &args); err != nil {
					args = nil
				}
			}

			if args == nil {
				args = map[string]any{}
			}

			// Use upstream ID for correlation, but ensure it exists
			id := c.ToolCall.ID
			if id == "" {
				id = generateFunctionCallID()
			}

			part := &Part{
				FunctionCall: &FunctionCall{
					ID:   id,
					Name: c.ToolCall.Name,
					Args: args,
				},
			}
			if pendingSig != "" {
				part.ThoughtSignature = google.DecodeThoughtSignature(pendingSig)
				pendingSig = ""
			}
			parts = append(parts, part)
		}
	}

	if pendingSig != "" {
		parts = append(parts, &Part{ThoughtSignature: google.DecodeThoughtSignature(pendingSig)})
	}

	if len(parts) == 0 {
		return nil
	}

	return &Content{
		Role:  "model",
		Parts: parts,
	}
}

func toFinishReason(completion *provider.Completion) FinishReason {
	if completion == nil {
		return FinishReasonStop
	}

	switch completion.StopReason {
	case provider.StopReasonMaxTokens, provider.StopReasonContextExceeded:
		return FinishReasonMaxTokens
	case provider.StopReasonRefusal:
		return FinishReasonSafety
	case provider.StopReasonEndTurn, provider.StopReasonStopSequence, provider.StopReasonToolUse:
		return FinishReasonStop
	}

	switch completion.Status {
	case provider.CompletionStatusIncomplete:
		return FinishReasonMaxTokens
	case provider.CompletionStatusRefused:
		return FinishReasonSafety
	case provider.CompletionStatusFailed:
		return FinishReasonOther
	}

	if completion.Message != nil {
		for _, c := range completion.Message.Content {
			if c.ToolCall != nil {
				// Gemini reports STOP for assistant turns that include function
				// calls; the SDK detects the calls in the candidate parts.
				return FinishReasonStop
			}
		}
	}

	return FinishReasonStop
}

// withTier adds effective processing metadata using Gemini's native spelling.
func withTier(metadata *UsageMetadata, tier provider.Tier) *UsageMetadata {
	native := serviceTier(tier)
	if native != "" {
		if metadata == nil {
			metadata = &UsageMetadata{}
		}
		metadata.Tier = native
	}
	return metadata
}

// toUsageMetadata maps the provider-level usage to Gemini's UsageMetadata.
// Returns nil when no usage data is available.
func toUsageMetadata(u *provider.Usage) *UsageMetadata {
	if u == nil {
		return nil
	}

	cached := u.CacheReadInputTokens
	total := u.InputTokens + u.OutputTokens
	var reasoningTokens int
	if u.ReasoningTokens != nil {
		reasoningTokens = *u.ReasoningTokens
	}

	// The intermediate OutputTokens is reasoning-inclusive, while Gemini's
	// CandidatesTokenCount counts only the visible response and reports thinking
	// separately in ThoughtsTokenCount.
	return &UsageMetadata{
		PromptTokenCount:        u.InputTokens,
		CachedContentTokenCount: cached,
		CandidatesTokenCount:    u.OutputTokens - reasoningTokens,
		ThoughtsTokenCount:      u.ReasoningTokens,
		TotalTokenCount:         total,
	}
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

// normalizeResponseSchema converts Gemini's OpenAPI-flavored responseSchema
// (uppercase TYPE enums, nullable, propertyOrdering) into plain JSON Schema so
// non-Gemini backends accept it. The input is not mutated.
func normalizeResponseSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}

	result := make(map[string]any, len(schema))

	for key, value := range schema {
		switch key {
		case "type":
			if t, ok := value.(string); ok {
				result[key] = strings.ToLower(t)
			} else {
				result[key] = value
			}

		case "propertyOrdering":
			// Gemini-only hint; other backends reject unknown keywords in
			// strict modes

		case "nullable":
			// folded into the type below

		case "properties", "$defs", "definitions":
			if m, ok := value.(map[string]any); ok {
				sub := make(map[string]any, len(m))
				for name, prop := range m {
					if ps, ok := prop.(map[string]any); ok {
						sub[name] = normalizeResponseSchema(ps)
					} else {
						sub[name] = prop
					}
				}
				result[key] = sub
			} else {
				result[key] = value
			}

		case "items", "not":
			if m, ok := value.(map[string]any); ok {
				result[key] = normalizeResponseSchema(m)
			} else {
				result[key] = value
			}

		case "anyOf", "allOf", "oneOf":
			if arr, ok := value.([]any); ok {
				sub := make([]any, len(arr))
				for i, item := range arr {
					if m, ok := item.(map[string]any); ok {
						sub[i] = normalizeResponseSchema(m)
					} else {
						sub[i] = item
					}
				}
				result[key] = sub
			} else {
				result[key] = value
			}

		default:
			result[key] = value
		}
	}

	if nullable, _ := schema["nullable"].(bool); nullable {
		if t, ok := result["type"].(string); ok && t != "null" {
			result["type"] = []any{t, "null"}
		}
	}

	return result
}
