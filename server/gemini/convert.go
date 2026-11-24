package gemini

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
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
		//lint:ignore ST1005 Preserve the client-facing Gemini error text.
		return nil, fmt.Errorf("Please use a valid role: user, model.")
	}

	var content []provider.Content

	for _, part := range c.Parts {
		if part == nil {
			continue
		}
		id, sig := provider.UnwrapReasoningID(string(part.ThoughtSignature))
		// Native Gemini signatures are binary; Interactions expects base64.
		// Wingman's realm-tagged signatures already hold the encoded value.
		if realm, _ := provider.ParseSignature(sig); realm == "" && id == "" {
			sig = google.EncodeThoughtSignature(part.ThoughtSignature)
		}

		if part.Thought {
			content = append(content, provider.ReasoningContent(provider.Reasoning{
				ID:        id,
				Content:   provider.TextParts(part.Text),
				Signature: sig,
			}))
			continue
		}

		// The signature precedes the part's content, including a function call:
		// Gemini requires it back on the call that opened the step.
		if sig != "" && part.FunctionResponse == nil {
			content = append(content, provider.ReasoningContent(provider.Reasoning{
				ID:        id,
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

			name, id := part.FunctionResponse.Name, part.FunctionResponse.ID
			queue := pendingCallIDs[name]
			if id == "" && len(queue) > 0 {
				id = queue[0]
			}
			if i := slices.Index(queue, id); i >= 0 {
				pendingCallIDs[name] = slices.Delete(queue, i, i+1)
			}
			if id == "" {
				id = generateFunctionCallID()
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

	// These bytes become conversation text; their order must survive replay.
	data, err := json.Marshal(v, json.Deterministic(true))
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
			// parameters uses Gemini's OpenAPI schema subset, including
			// uppercase types from python-genai's types.Schema. Other backends
			// need JSON Schema, which parametersJsonSchema already is.
			var params map[string]any
			if fd.Parameters != nil {
				if p, ok := fd.Parameters.(map[string]any); ok {
					params = normalizeResponseSchema(p)
				}
			} else if p, ok := fd.ParametersJsonSchema.(map[string]any); ok {
				params = p
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

// thoughtSignature keeps the original reasoning item ID for Responses replay.
func thoughtSignature(r provider.Reasoning) []byte {
	return google.DecodeThoughtSignature(provider.WrapReasoningID(r.ID, r.Signature))
}

func toCandidate(content []provider.Content, finish FinishReason) *Candidate {
	var parts []*Part

	var pendingSig []byte

	for i, c := range content {
		if c.Reasoning != nil {
			if len(pendingSig) > 0 {
				parts = append(parts, &Part{ThoughtSignature: pendingSig})
				pendingSig = nil
			}
			beforeCall := i+1 < len(content) && content[i+1].ToolCall != nil
			text := c.Reasoning.Text()
			if text == "" {
				text = c.Reasoning.SummaryText()
			}

			if text == "" {
				if c.Reasoning.Signature != "" {
					// A replayed signature precedes the part it belongs to; a
					// terminal signature-only delta belongs to the preceding part.
					if n := len(parts); c.Reasoning.ID == "" && i == len(content)-1 && n > 0 && len(parts[n-1].ThoughtSignature) == 0 && !parts[n-1].Thought {
						parts[n-1].ThoughtSignature = thoughtSignature(*c.Reasoning)
					} else {
						pendingSig = thoughtSignature(*c.Reasoning)
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
					pendingSig = thoughtSignature(*c.Reasoning)
				} else {
					part.ThoughtSignature = thoughtSignature(*c.Reasoning)
				}
			}

			parts = append(parts, part)
		}

		if text := c.Text + c.Refusal; text != "" {
			part := &Part{
				Text: text,
			}
			if len(pendingSig) > 0 {
				part.ThoughtSignature = pendingSig
				pendingSig = nil
			}
			parts = append(parts, part)
		}

		if c.ToolCall != nil && finish == FinishReasonStop {
			args := map[string]any{}
			if c.ToolCall.Arguments != "" {
				if err := decodeJSON(strings.NewReader(c.ToolCall.Arguments), &args); err != nil || args == nil {
					finish = FinishReasonMalformedFunctionCall
					continue
				}
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
			if len(pendingSig) > 0 {
				part.ThoughtSignature = pendingSig
				pendingSig = nil
			}
			parts = append(parts, part)
		}
	}

	if len(pendingSig) > 0 {
		parts = append(parts, &Part{ThoughtSignature: pendingSig})
	}

	if finish == FinishReasonMalformedFunctionCall {
		// SDKs execute function calls as soon as they see the parts.
		parts = slices.DeleteFunc(parts, func(p *Part) bool { return p.FunctionCall != nil })
	}
	candidate := &Candidate{FinishReason: finish}
	if len(parts) > 0 {
		candidate.Content = &Content{Role: "model", Parts: parts}
	}
	return candidate
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
	}

	switch completion.Status {
	case provider.CompletionStatusIncomplete:
		return FinishReasonMaxTokens
	case provider.CompletionStatusRefused:
		return FinishReasonSafety
	case provider.CompletionStatusFailed:
		return FinishReasonOther
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

// normalizeResponseSchema converts Gemini's OpenAPI schemas for tools and
// structured output into JSON Schema.
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

		case "minItems", "maxItems", "minLength", "maxLength", "minProperties", "maxProperties":
			// These are int64 fields in ProtoJSON, often sent as strings.
			if s, ok := value.(string); ok {
				if n, ok := jsonutil.Int64(jsonutil.Number(s)); ok {
					value = n
				}
			}
			result[key] = value

		case "ref":
			if ref, ok := value.(string); ok {
				if path, ok := strings.CutPrefix(ref, "#/defs/"); ok {
					value = "#/$defs/" + path
				}
			}
			result["$ref"] = value

		case "properties", "defs", "$defs", "definitions":
			if key == "defs" {
				key = "$defs"
			}
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

		case "items", "not", "additionalProperties":
			if m, ok := value.(map[string]any); ok {
				result[key] = normalizeResponseSchema(m)
			} else {
				result[key] = value
			}

		case "anyOf", "allOf", "oneOf", "prefixItems":
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
