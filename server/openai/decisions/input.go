package decisions

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/server/files"
)

func parseInput(raw jsontext.Value) ([]provider.Message, error) {
	if absent(raw) {
		return nil, errors.New("input is required")
	}
	var text string
	if raw.Kind() == '"' && json.Unmarshal(raw, &text) == nil {
		return []provider.Message{provider.UserMessage(text)}, nil
	}
	var items []struct {
		Role      string         `json:"role"`
		Type      string         `json:"type"`
		Content   jsontext.Value `json:"content"`
		CallID    string         `json:"call_id"`
		Name      string         `json:"name"`
		Arguments *string        `json:"arguments"`
		Input     *string        `json:"input"`
		Output    jsontext.Value `json:"output"`
	}
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		return nil, errors.New("input must be text or an array of messages and tool items")
	}
	calls := map[string]string{}
	outputs := map[string]bool{}
	var messages []provider.Message
	totalImages := 0
	for i, item := range items {
		switch item.Type {
		case "", "message":
			var role provider.MessageRole
			switch item.Role {
			case "user":
				role = provider.MessageRoleUser
			case "assistant":
				role = provider.MessageRoleAssistant
			case "developer", "system":
				role = provider.MessageRoleSystem
			default:
				return nil, fmt.Errorf("input[%d]: unsupported role %q", i, item.Role)
			}
			parts, imgs, err := parseParts(item.Content, item.Role)
			if err != nil {
				return nil, fmt.Errorf("input[%d].content: %w", i, err)
			}
			totalImages += imgs
			messages = append(messages, provider.Message{
				Role:    role,
				Content: parts,
			})
		case "function_call", "custom_tool_call":
			if item.CallID == "" || item.Name == "" || calls[item.CallID] != "" {
				return nil, fmt.Errorf("input[%d]: tool call requires a unique call_id and name", i)
			}
			var args string
			kind := provider.ToolKindFunction
			if item.Type == "function_call" {
				if item.Arguments == nil {
					return nil, fmt.Errorf("input[%d]: tool call arguments or input are required", i)
				}
				args = *item.Arguments
			} else {
				if item.Input == nil {
					return nil, fmt.Errorf("input[%d]: tool call arguments or input are required", i)
				}
				args = *item.Input
				kind = provider.ToolKindCustom
			}
			calls[item.CallID] = item.Type
			messages = append(messages, provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{
					provider.ToolCallContent(provider.ToolCall{
						ID:        item.CallID,
						Kind:      kind,
						Name:      item.Name,
						Arguments: args,
					}),
				},
			})
		case "function_call_output", "custom_tool_call_output":
			callType := strings.TrimSuffix(item.Type, "_output")
			if calls[item.CallID] != callType || outputs[item.CallID] {
				return nil, fmt.Errorf("input[%d]: tool output requires a matching earlier call", i)
			}
			outputs[item.CallID] = true
			contents, imgs, err := parseParts(item.Output, "tool")
			if err != nil {
				return nil, fmt.Errorf("input[%d].output: %w", i, err)
			}
			totalImages += imgs
			var toolParts []provider.Part
			for _, c := range contents {
				if c.File != nil {
					toolParts = append(toolParts, provider.Part{File: c.File})
				} else {
					toolParts = append(toolParts, provider.Part{Text: c.Text})
				}
			}
			kind := provider.ToolKindFunction
			if callType == "custom_tool_call" {
				kind = provider.ToolKindCustom
			}
			messages = append(messages, provider.Message{
				Role: provider.MessageRoleUser,
				Content: []provider.Content{
					provider.ToolResultContent(provider.ToolResult{
						ID:    item.CallID,
						Kind:  kind,
						Parts: toolParts,
					}),
				},
			})
		default:
			return nil, fmt.Errorf("input[%d]: unsupported type %q", i, item.Type)
		}
	}
	if totalImages > 128 {
		return nil, errors.New("at most 128 images are supported in input")
	}
	return messages, nil
}

func absent(raw jsontext.Value) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func parseParts(raw jsontext.Value, role string) ([]provider.Content, int, error) {
	if absent(raw) {
		return nil, 0, errors.New("content is required")
	}
	var text string
	if raw.Kind() == '"' && json.Unmarshal(raw, &text) == nil {
		return []provider.Content{provider.TextContent(text)}, 0, nil
	}
	var parts []struct {
		Type       string                `json:"type"`
		Text       *string               `json:"text"`
		Refusal    *string               `json:"refusal"`
		Data       jsontext.Value        `json:"data"`
		ImageURL   *string               `json:"image_url"`
		FileID     *string               `json:"file_id"`
		Detail     *provider.ImageDetail `json:"detail"`
		Breakpoint jsontext.Value        `json:"prompt_cache_breakpoint"`
	}
	if json.Unmarshal(raw, &parts) != nil || len(parts) == 0 {
		return nil, 0, errors.New("content must be text or a nonempty array of parts")
	}
	result := make([]provider.Content, 0, len(parts))
	images := 0
	for _, part := range parts {
		if !absent(part.Breakpoint) {
			var breakpoint struct {
				Mode string `json:"mode"`
			}
			if role == "question" || (part.Type != "input_text" && part.Type != "input_image") || json.Unmarshal(part.Breakpoint, &breakpoint) != nil || breakpoint.Mode != "explicit" {
				return nil, 0, errors.New("cache breakpoints require mode explicit on shared input text or images")
			}
		}
		switch part.Type {
		case "input_text":
			if role == "assistant" || part.Text == nil {
				return nil, 0, errors.New("input_text requires text and a non-assistant role")
			}
			c := provider.TextContent(*part.Text)
			c.TextPresent = true
			if !absent(part.Breakpoint) {
				c.CacheControl = &provider.CacheControl{}
			}
			result = append(result, c)
		case "output_text":
			if role != "assistant" || part.Text == nil {
				return nil, 0, errors.New("output_text requires text and an assistant role")
			}
			c := provider.TextContent(*part.Text)
			c.TextPresent = true
			if !absent(part.Breakpoint) {
				c.CacheControl = &provider.CacheControl{}
			}
			result = append(result, c)
		case "refusal":
			if role != "assistant" || part.Refusal == nil {
				return nil, 0, errors.New("refusal requires refusal text and an assistant role")
			}
			result = append(result, provider.RefusalContent(*part.Refusal))
		case "input_json":
			data := bytes.TrimSpace(part.Data)
			if role != "user" && role != "question" || len(data) == 0 || data[0] != '{' && data[0] != '[' {
				return nil, 0, errors.New("input_json requires object or array data in a user message or question")
			}
			if err := validateJSONData(data); err != nil {
				return nil, 0, err
			}
			result = append(result, provider.Content{Text: string(data), JSON: string(part.Data)})
		case "input_image":
			if role != "user" && role != "tool" && role != "question" {
				return nil, 0, errors.New("input_image is only supported in user messages, tool outputs, and questions")
			}
			if part.Detail != nil {
				switch *part.Detail {
				case provider.ImageDetailLow, provider.ImageDetailHigh, provider.ImageDetailAuto, provider.ImageDetailOriginal:
				default:
					return nil, 0, fmt.Errorf("invalid input_image detail %q", *part.Detail)
				}
			}
			if (part.ImageURL == nil || *part.ImageURL == "") && (part.FileID == nil || *part.FileID == "") {
				return nil, 0, errors.New("input_image requires image_url or file_id")
			}
			if part.ImageURL != nil && *part.ImageURL != "" && part.FileID != nil && *part.FileID != "" {
				return nil, 0, errors.New("input_image cannot specify both image_url and file_id")
			}
			if part.FileID != nil && *part.FileID != "" {
				return nil, 0, errors.New("input_image file_id is not supported")
			}
			if !strings.HasPrefix(*part.ImageURL, "data:image/") {
				u, err := url.Parse(*part.ImageURL)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return nil, 0, errors.New("input_image requires an HTTP(S) URL or image data URL")
				}
			}
			file, err := files.FromURL(*part.ImageURL)
			if err != nil {
				return nil, 0, err
			}
			images++
			if !strings.HasPrefix(file.ContentType, "image/") {
				return nil, 0, errors.New("input_image must contain image data")
			}
			if part.Detail != nil {
				file.ImageDetail = *part.Detail
			}
			c := provider.FileContent(file)
			if !absent(part.Breakpoint) {
				c.CacheControl = &provider.CacheControl{}
			}
			result = append(result, c)
		default:
			return nil, 0, fmt.Errorf("unsupported content type %q", part.Type)
		}
	}
	return result, images, nil
}

func parseQuestionInstructions(raw jsontext.Value) (any, error) {
	if absent(raw) {
		return nil, errors.New("instructions is required")
	}
	var text string
	if raw.Kind() == '"' && json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	parts, _, err := parseParts(raw, "question")
	if err != nil {
		return nil, err
	}
	return parts, nil
}

// Match the Decisions JSON input limits before falling back to text adapters.
func validateJSONData(raw jsontext.Value) error {
	var data any
	if err := jsonutil.Unmarshal(raw, &data); err != nil {
		return err
	}
	compact, err := json.Marshal(data)
	if err != nil || len(compact) > 256*1024 {
		return errors.New("input_json exceeds 256 KiB")
	}
	if a, ok := data.([]any); ok {
		for _, item := range a {
			if _, ok := item.(map[string]any); !ok {
				return errors.New("input_json arrays must contain objects")
			}
		}
	}
	count := 0
	var walk func(any, int) error
	walk = func(v any, depth int) error {
		count++
		if count > 10000 {
			return errors.New("input_json exceeds 10000 values")
		}
		switch value := v.(type) {
		case map[string]any:
			if depth >= 32 {
				return errors.New("input_json exceeds 32 container levels")
			}
			for _, child := range value {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		case []any:
			if depth >= 32 {
				return errors.New("input_json exceeds 32 container levels")
			}
			for _, child := range value {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(data, 0)
}
