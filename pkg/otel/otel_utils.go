package otel

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/semconv/v1.40.0/genaiconv"
)

// Cache token type attributes following the GenAI semantic conventions:
// gen_ai.usage.cache_creation.input_tokens and gen_ai.usage.cache_read.input_tokens
var (
	TokenTypeCacheCreation genaiconv.TokenTypeAttr = "cache_creation"
	TokenTypeCacheRead     genaiconv.TokenTypeAttr = "cache_read"
)

type KeyValue = attribute.KeyValue

func String(key string, val string) KeyValue {
	return attribute.String(key, val)
}

func Strings(key string, val []string) KeyValue {
	return attribute.StringSlice(key, val)
}

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

func EndUserAttrs(ctx context.Context) []KeyValue {
	var attrs []KeyValue

	if user, ok := ctx.Value(auth.UserContextKey).(string); ok && user != "" {
		attrs = append(attrs,
			attribute.String("user.id", user),
			attribute.String("enduser.id", user), // deprecated
		)
	}

	if email, ok := ctx.Value(auth.EmailContextKey).(string); ok && email != "" {
		attrs = append(attrs,
			attribute.String("user.email", email),
			attribute.String("enduser.email", email), // deprecated
		)
	}

	if name, ok := ctx.Value(auth.NameContextKey).(string); ok && name != "" {
		attrs = append(attrs,
			attribute.String("user.full_name", name),
			attribute.String("enduser.name", name), // deprecated
		)
	}

	if session, ok := ctx.Value(auth.SessionContextKey).(string); ok && session != "" {
		attrs = append(attrs,
			attribute.String("session.id", session),
		)
	}

	return attrs
}

func RequestAttrs(operation attribute.KeyValue, providerName, requestModel, responseModel string) []KeyValue {
	attrs := []KeyValue{
		operation,
	}

	if providerName != "" {
		attrs = append(attrs, semconv.GenAIProviderNameKey.String(providerName))
	}

	if requestModel != "" {
		attrs = append(attrs, semconv.GenAIRequestModel(requestModel))
	}

	if responseModel != "" {
		attrs = append(attrs, semconv.GenAIResponseModel(responseModel))
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

	data, err := json.Marshal(flattenMessages(messages))

	if err != nil {
		return nil
	}

	return []KeyValue{attribute.String("gen_ai.prompt", string(data))}
}

// Gated by EnableDebug to avoid leaking user data.
func CompletionAttrs(completion *provider.Completion) []KeyValue {
	if !EnableDebug || completion == nil || completion.Message == nil {
		return nil
	}

	data, err := json.Marshal(flattenMessages([]provider.Message{*completion.Message}))

	if err != nil {
		return nil
	}

	return []KeyValue{attribute.String("gen_ai.completion", string(data))}
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

func flattenMessages(messages []provider.Message) []map[string]string {
	result := make([]map[string]string, 0, len(messages))

	for _, m := range messages {
		var text strings.Builder

		for _, c := range m.Content {
			text.WriteString(c.Text)
			text.WriteString(c.Refusal)
		}

		result = append(result, map[string]string{
			"role":    string(m.Role),
			"content": text.String(),
		})
	}

	return result
}
