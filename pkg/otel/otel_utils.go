package otel

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net"
	"strconv"
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
	span.SetAttributes(attribute.String(string(semconv.ErrorTypeKey), normalizeErrorType(err)))
}

func ErrorTypeAttr(err error) genaiconv.ErrorTypeAttr {
	return genaiconv.ErrorTypeAttr(normalizeErrorType(err))
}

// normalizeErrorType maps a Go error to a low-cardinality error.type value:
// HTTP status string for ProviderError, well-known classes for cancellation /
// timeout / DNS / connection failures, and "_OTHER" as the spec-defined fallback.
// Using runtime type names (the default semconv.ErrorType behaviour) blows up
// histogram cardinality.
func normalizeErrorType(err error) string {
	if err == nil {
		return ""
	}

	var provErr *provider.ProviderError
	if stderrors.As(err, &provErr) && provErr.Code > 0 {
		return strconv.Itoa(provErr.Code)
	}

	if stderrors.Is(err, context.Canceled) {
		return "canceled"
	}

	if stderrors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}

	var netErr net.Error
	if stderrors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}

	var dnsErr *net.DNSError
	if stderrors.As(err, &dnsErr) {
		return "dns_error"
	}

	var opErr *net.OpError
	if stderrors.As(err, &opErr) {
		return "connection_error"
	}

	return string(genaiconv.ErrorTypeOther)
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

	return attrs
}

func MetricAttrs(ctx context.Context, requestModel, responseModel string) []KeyValue {
	return KeyValues(
		[]KeyValue{
			semconv.GenAIRequestModel(requestModel),
			semconv.GenAIResponseModel(responseModel),
		},
		EndUserAttrs(ctx),
	)
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

	if usage.ReasoningTokens > 0 {
		attrs = append(attrs, semconv.GenAIUsageReasoningOutputTokens(usage.ReasoningTokens))
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
// System messages are split out into gen_ai.system_instructions (see
// SystemInstructionsAttrs) per the current GenAI semantic convention; only
// user / assistant / tool messages remain in gen_ai.input.messages.
func PromptAttrs(messages []provider.Message) []KeyValue {
	if !EnableDebug {
		return nil
	}

	chats := make([]chatMessage, 0, len(messages))
	for _, m := range messages {
		if m.Role == provider.MessageRoleSystem {
			continue
		}
		chats = append(chats, toChatMessage(m))
	}

	data, err := json.Marshal(chats)

	if err != nil {
		return nil
	}

	return []KeyValue{semconv.GenAIInputMessagesKey.String(string(data))}
}

// Gated by EnableDebug to avoid leaking user data.
// Spec shape: an array of message parts (currently text only) representing the
// system prompt, kept separate from gen_ai.input.messages.
func SystemInstructionsAttrs(messages []provider.Message) []KeyValue {
	if !EnableDebug {
		return nil
	}

	var parts []messagePart
	for _, m := range messages {
		if m.Role != provider.MessageRoleSystem {
			continue
		}
		for _, c := range m.Content {
			if c.Text != "" {
				parts = append(parts, messagePart{Type: "text", Content: c.Text})
			}
		}
	}

	if len(parts) == 0 {
		return nil
	}

	data, err := json.Marshal(parts)

	if err != nil {
		return nil
	}

	return []KeyValue{semconv.GenAISystemInstructionsKey.String(string(data))}
}

// Gated by EnableDebug. Spec shape: array of { type, name, description, parameters }.
func ToolDefinitionsAttrs(tools []provider.Tool) []KeyValue {
	if !EnableDebug || len(tools) == 0 {
		return nil
	}

	defs := make([]toolDefinition, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, toolDefinition{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}

	data, err := json.Marshal(defs)

	if err != nil {
		return nil
	}

	return []KeyValue{semconv.GenAIToolDefinitionsKey.String(string(data))}
}

// RequestOptionAttrs surfaces gen_ai.request.* span attributes from the
// provider CompleteOptions. Spec marks these "Recommended if applicable".
func RequestOptionAttrs(options *provider.CompleteOptions) []KeyValue {
	if options == nil {
		return nil
	}

	var attrs []KeyValue

	if options.Temperature != nil {
		attrs = append(attrs, semconv.GenAIRequestTemperature(float64(*options.Temperature)))
	}

	if options.MaxTokens != nil {
		attrs = append(attrs, semconv.GenAIRequestMaxTokens(*options.MaxTokens))
	}

	if len(options.Stop) > 0 {
		attrs = append(attrs, semconv.GenAIRequestStopSequences(options.Stop...))
	}

	return attrs
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
	FinishReason string `json:"finish_reason"`
}

type messagePart struct {
	Type string `json:"type"`

	Content   string `json:"content,omitempty"`   // text / reasoning
	ID        string `json:"id,omitempty"`        // tool_call / tool_call_response
	Name      string `json:"name,omitempty"`      // tool_call
	Arguments any    `json:"arguments,omitempty"` // tool_call
	Response  any    `json:"response,omitempty"`  // tool_call_response
}

// toolDefinition mirrors the GenAI semantic convention gen_ai.tool.definitions
// shape: an array of available tools advertised on the request.
type toolDefinition struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
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
			var response any = text.String()
			if s := text.String(); s != "" {
				var parsed any
				if err := json.Unmarshal([]byte(s), &parsed); err == nil {
					response = parsed
				}
			}
			msg.Parts = append(msg.Parts, messagePart{
				Type:     "tool_call_response",
				ID:       c.ToolResult.ID,
				Response: response,
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
