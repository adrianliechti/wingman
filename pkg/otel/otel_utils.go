package otel

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/semconv/v1.41.0/genaiconv"
	"go.opentelemetry.io/otel/trace"
)

type KeyValue = attribute.KeyValue

func KeyValues(attrs ...[]KeyValue) []KeyValue {
	var result []KeyValue

	for _, a := range attrs {
		result = append(result, a...)
	}

	return result
}

func Label(ctx context.Context, attrs ...KeyValue) {
	labeler, ok := otelhttp.LabelerFromContext(ctx)

	if !ok {
		return
	}

	labeler.Add(attrs...)
}

func RecordError(span trace.Span, err error) {
	if err == nil {
		return
	}

	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	span.SetAttributes(semconv.ErrorType(err))
}

func ErrorTypeAttr(err error) genaiconv.ErrorTypeAttr {
	return genaiconv.ErrorTypeAttr(semconv.ErrorType(err).Value.AsString())
}

func GenAISpanName(operation genaiconv.OperationNameAttr, model string) string {
	if model == "" {
		return string(operation)
	}

	return string(operation) + " " + model
}

func EndUserAttrs(ctx context.Context) []KeyValue {
	var attrs []KeyValue

	if user, ok := ctx.Value(auth.UserContextKey).(string); ok && user != "" {
		attrs = append(attrs, attribute.String("user.id", user))
	}

	if email, ok := ctx.Value(auth.EmailContextKey).(string); ok && email != "" {
		attrs = append(attrs, attribute.String("user.email", email))
	}

	if name, ok := ctx.Value(auth.NameContextKey).(string); ok && name != "" {
		attrs = append(attrs, attribute.String("user.full_name", name))
	}

	if session, ok := ctx.Value(auth.SessionContextKey).(string); ok && session != "" {
		attrs = append(attrs, attribute.String("session.id", session))
	}

	return attrs
}

func RequestAttrs(operation attribute.KeyValue, providerName, requestModel string) []KeyValue {
	attrs := []KeyValue{
		operation,
	}

	if providerName != "" {
		attrs = append(attrs, semconv.GenAIProviderNameKey.String(providerName))
	}

	if requestModel != "" {
		attrs = append(attrs, semconv.GenAIRequestModel(requestModel))
	}

	return attrs
}

func UsageAttrs(usage *provider.Usage) []KeyValue {
	if usage == nil {
		return nil
	}

	var attrs []KeyValue

	if usage.InputTokens > 0 {
		attrs = append(attrs, semconv.GenAIUsageInputTokens(usage.InputTokens))
	}

	if usage.OutputTokens > 0 {
		attrs = append(attrs, semconv.GenAIUsageOutputTokens(usage.OutputTokens))
	}

	if usage.CacheCreationInputTokens > 0 {
		attrs = append(attrs, semconv.GenAIUsageCacheCreationInputTokens(usage.CacheCreationInputTokens))
	}

	if usage.CacheReadInputTokens > 0 {
		attrs = append(attrs, semconv.GenAIUsageCacheReadInputTokens(usage.CacheReadInputTokens))
	}

	return attrs
}

// Gated by EnableDebug to avoid leaking user data.
func PromptAttrs(messages []provider.Message) []KeyValue {
	if !EnableDebug {
		return nil
	}

	chats := make([]chatMessage, 0, len(messages))
	for _, m := range messages {
		chats = append(chats, toChatMessage(m))
	}

	data, err := json.Marshal(chats)

	if err != nil {
		return nil
	}

	return []KeyValue{semconv.GenAIInputMessagesKey.String(string(data))}
}

// Gated by EnableDebug to avoid leaking user data.
func CompletionAttrs(completion *provider.Completion) []KeyValue {
	if !EnableDebug || completion == nil || completion.Message == nil {
		return nil
	}

	out := outputMessage{
		chatMessage:  toChatMessage(*completion.Message),
		FinishReason: finishReason(completion),
	}

	data, err := json.Marshal([]outputMessage{out})

	if err != nil {
		return nil
	}

	return []KeyValue{semconv.GenAIOutputMessagesKey.String(string(data))}
}

// Gated by EnableDebug to avoid leaking user data.
func ToolArgumentAttrs(parameters map[string]any) []KeyValue {
	if !EnableDebug || parameters == nil {
		return nil
	}

	data, err := json.Marshal(parameters)

	if err != nil {
		return nil
	}

	return []KeyValue{semconv.GenAIToolCallArgumentsKey.String(string(data))}
}

// Gated by EnableDebug to avoid leaking user data.
func ToolResultAttrs(result any) []KeyValue {
	if !EnableDebug || result == nil {
		return nil
	}

	data, err := json.Marshal(result)

	if err != nil {
		return nil
	}

	return []KeyValue{semconv.GenAIToolCallResultKey.String(string(data))}
}

// chatMessage / outputMessage / messagePart mirror the GenAI semantic
// convention message shapes (gen_ai.input.messages, gen_ai.output.messages):
// role + array of typed parts. Role "tool" is used when a message carries
// tool results.
type chatMessage struct {
	Role  string        `json:"role"`
	Parts []messagePart `json:"parts"`
}

type outputMessage struct {
	chatMessage
	FinishReason string `json:"finish_reason,omitempty"`
}

type messagePart struct {
	Type string `json:"type"`

	Content   string `json:"content,omitempty"`   // text / reasoning
	ID        string `json:"id,omitempty"`        // tool_call / tool_call_response
	Name      string `json:"name,omitempty"`      // tool_call
	Arguments any    `json:"arguments,omitempty"` // tool_call
	Response  any    `json:"response,omitempty"`  // tool_call_response
}

func toChatMessage(m provider.Message) chatMessage {
	msg := chatMessage{Role: string(m.Role)}

	for _, c := range m.Content {
		if c.ToolResult != nil {
			msg.Role = "tool"
			break
		}
	}

	for _, c := range m.Content {
		if c.Text != "" {
			msg.Parts = append(msg.Parts, messagePart{Type: "text", Content: c.Text})
		}

		if c.Refusal != "" {
			msg.Parts = append(msg.Parts, messagePart{Type: "text", Content: c.Refusal})
		}

		if c.Reasoning != nil {
			text := c.Reasoning.Text
			if text == "" {
				text = c.Reasoning.Summary
			}
			if text != "" {
				msg.Parts = append(msg.Parts, messagePart{Type: "reasoning", Content: text})
			}
		}

		if c.ToolCall != nil {
			var args any = c.ToolCall.Arguments
			if c.ToolCall.Arguments != "" {
				var parsed any
				if err := json.Unmarshal([]byte(c.ToolCall.Arguments), &parsed); err == nil {
					args = parsed
				}
			}
			msg.Parts = append(msg.Parts, messagePart{
				Type:      "tool_call",
				ID:        c.ToolCall.ID,
				Name:      c.ToolCall.Name,
				Arguments: args,
			})
		}

		if c.ToolResult != nil {
			var text strings.Builder
			for _, p := range c.ToolResult.Parts {
				text.WriteString(p.Text)
			}
			msg.Parts = append(msg.Parts, messagePart{
				Type:     "tool_call_response",
				ID:       c.ToolResult.ID,
				Response: text.String(),
			})
		}
	}

	return msg
}

// finishReason maps a provider CompletionStatus to the GenAI semantic
// convention finish_reason enum (stop / length / content_filter / tool_call / error).
func finishReason(c *provider.Completion) string {
	switch c.Status {
	case provider.CompletionStatusIncomplete:
		return "length"
	case provider.CompletionStatusFailed:
		return "error"
	case provider.CompletionStatusRefused:
		return "content_filter"
	}

	if c.Message != nil {
		for _, content := range c.Message.Content {
			if content.ToolCall != nil {
				return "tool_call"
			}
		}
	}

	return "stop"
}
