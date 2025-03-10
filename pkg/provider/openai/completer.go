package openai

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go"
)

var _ provider.Completer = (*Completer)(nil)

type Completer struct {
	*Config
	completions *openai.ChatCompletionService
}

func NewCompleter(url, model string, options ...Option) (*Completer, error) {
	cfg := &Config{
		url:   url,
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Completer{
		Config:      cfg,
		completions: openai.NewChatCompletionService(cfg.Options()...),
	}, nil
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) (*provider.Completion, error) {
	if options == nil {
		options = new(provider.CompleteOptions)
	}

	req, err := c.convertCompletionRequest(messages, options)

	if err != nil {
		return nil, err
	}

	if options.Stream != nil {
		return c.completeStream(ctx, *req, options)
	}

	return c.complete(ctx, *req, options)
}

func (c *Completer) complete(ctx context.Context, req openai.ChatCompletionNewParams, options *provider.CompleteOptions) (*provider.Completion, error) {
	completion, err := c.completions.New(ctx, req)

	if err != nil {
		return nil, convertError(err)
	}

	choice := completion.Choices[0]
	reason := toCompletionResult(choice.FinishReason)

	if reason == "" {
		reason = provider.CompletionReasonStop
	}

	return &provider.Completion{
		ID:     completion.ID,
		Reason: reason,

		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: choice.Message.Content,

			ToolCalls: toToolCalls(choice.Message.ToolCalls),
		},

		Usage: &provider.Usage{
			InputTokens:  int(completion.Usage.PromptTokens),
			OutputTokens: int(completion.Usage.CompletionTokens),
		},
	}, nil
}

func (c *Completer) completeStream(ctx context.Context, req openai.ChatCompletionNewParams, options *provider.CompleteOptions) (*provider.Completion, error) {
	stream := c.completions.NewStreaming(ctx, req)

	result := openai.ChatCompletionAccumulator{}

	var usage *openai.CompletionUsage

	for stream.Next() {
		chunk := stream.Current()
		result.AddChunk(chunk)

		delta := provider.Completion{
			ID: result.ID,

			Message: &provider.Message{
				Role: provider.MessageRoleAssistant,
			},
		}

		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]

			delta.Reason = toDeltaCompletionResult(choice.FinishReason)

			delta.Message.Content = choice.Delta.Content
			delta.Message.Refusal = choice.Delta.Refusal

			delta.Message.ToolCalls = toDeltaToolCalls(choice.Delta.ToolCalls)
		}

		if chunk.Usage.TotalTokens > 0 {
			usage = &chunk.Usage

			delta.Usage = &provider.Usage{
				InputTokens:  int(chunk.Usage.PromptTokens),
				OutputTokens: int(chunk.Usage.CompletionTokens),
			}
		}

		if err := options.Stream(ctx, delta); err != nil {
			return nil, err
		}
	}

	if err := stream.Err(); err != nil {
		return nil, convertError(err)
	}

	choice := result.Choices[0]
	reason := toCompletionResult(choice.FinishReason)

	if reason == "" {
		reason = provider.CompletionReasonStop
	}

	completion := &provider.Completion{
		ID:     result.ID,
		Reason: reason,

		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
		},
	}

	if len(result.Choices) > 0 {
		completion.Message.Content = choice.Message.Content
		completion.Message.Refusal = choice.Message.Refusal

		completion.Message.ToolCalls = toToolCalls(choice.Message.ToolCalls)
	}

	if result.Usage.TotalTokens > 0 {
		usage = &result.Usage
	}

	if usage != nil && usage.TotalTokens > 0 {
		completion.Usage = &provider.Usage{
			InputTokens:  int(usage.PromptTokens),
			OutputTokens: int(usage.CompletionTokens),
		}
	}

	return completion, nil
}

func (c *Completer) convertCompletionRequest(input []provider.Message, options *provider.CompleteOptions) (*openai.ChatCompletionNewParams, error) {
	if options == nil {
		options = new(provider.CompleteOptions)
	}

	tools, err := convertTools(options.Tools)

	if err != nil {
		return nil, err
	}

	messages, err := c.convertMessages(input)

	if err != nil {
		return nil, err
	}

	req := &openai.ChatCompletionNewParams{
		Model: openai.F(c.model),
	}

	if options.Stream != nil {
		if !strings.Contains(c.url, "api.mistral.ai") {
			req.StreamOptions = openai.F(openai.ChatCompletionStreamOptionsParam{
				IncludeUsage: openai.F(true),
			})
		}
	}

	if len(tools) > 0 {
		req.Tools = openai.F(tools)
	}

	if len(messages) > 0 {
		req.Messages = openai.F(messages)
	}

	switch options.Effort {
	case provider.ReasoningEffortLow:
		req.ReasoningEffort = openai.F(openai.ChatCompletionReasoningEffortLow)

	case provider.ReasoningEffortMedium:
		req.ReasoningEffort = openai.F(openai.ChatCompletionReasoningEffortMedium)

	case provider.ReasoningEffortHigh:
		req.ReasoningEffort = openai.F(openai.ChatCompletionReasoningEffortHigh)
	}

	if options.Format == provider.CompletionFormatJSON {
		req.ResponseFormat = openai.F[openai.ChatCompletionNewParamsResponseFormatUnion](openai.ResponseFormatJSONObjectParam{
			Type: openai.F(openai.ResponseFormatJSONObjectTypeJSONObject),
		})
	}

	if options.Schema != nil {
		schema := openai.ResponseFormatJSONSchemaJSONSchemaParam{
			Name:   openai.F(options.Schema.Name),
			Schema: openai.F(any(options.Schema.Schema)),
		}

		if options.Schema.Description != "" {
			schema.Description = openai.F(options.Schema.Description)
		}

		if options.Schema.Strict != nil {
			schema.Strict = openai.F(*options.Schema.Strict)
		}

		req.ResponseFormat = openai.F[openai.ChatCompletionNewParamsResponseFormatUnion](openai.ResponseFormatJSONSchemaParam{
			Type: openai.F(openai.ResponseFormatJSONSchemaTypeJSONSchema),

			JSONSchema: openai.F(schema),
		})
	}

	if options.Stop != nil {
		stops := openai.ChatCompletionNewParamsStopArray(options.Stop)
		req.Stop = openai.F[openai.ChatCompletionNewParamsStopUnion](stops)
	}

	if options.MaxTokens != nil {
		if slices.Contains([]string{"o1", "o1-mini", "o3-mini"}, c.model) {
			req.MaxCompletionTokens = openai.F(int64(*options.MaxTokens))
		} else {
			req.MaxTokens = openai.F(int64(*options.MaxTokens))
		}
	}

	if options.Temperature != nil {
		req.Temperature = openai.F(float64(*options.Temperature))
	}

	return req, nil
}

func (c *Completer) convertMessages(input []provider.Message) ([]openai.ChatCompletionMessageParamUnion, error) {
	var result []openai.ChatCompletionMessageParamUnion

	for _, m := range input {
		switch m.Role {
		case provider.MessageRoleSystem:
			message := openai.SystemMessage(m.Content)

			if slices.Contains([]string{"o1", "o1-mini", "o3-mini"}, c.model) {
				message = openai.ChatCompletionDeveloperMessageParam{
					Role: openai.F(openai.ChatCompletionDeveloperMessageParamRoleDeveloper),

					Content: openai.F([]openai.ChatCompletionContentPartTextParam{
						openai.TextPart(m.Content),
					}),
				}
			}

			result = append(result, message)

		case provider.MessageRoleUser:
			parts := []openai.ChatCompletionContentPartUnionParam{}

			if m.Content != "" {
				parts = append(parts, openai.TextPart(m.Content))
			}

			for _, f := range m.Files {
				data, err := io.ReadAll(f.Content)

				if err != nil {
					return nil, err
				}

				mime := f.ContentType
				content := base64.StdEncoding.EncodeToString(data)

				switch f.ContentType {
				case "image/png", "image/jpeg", "image/webp", "image/gif":
					url := "data:" + mime + ";base64," + content
					parts = append(parts, openai.ImagePart(url))

				default:
					return nil, errors.New("unsupported content type")
				}
			}

			message := openai.UserMessageParts(parts...)
			result = append(result, message)

		case provider.MessageRoleAssistant:
			message := openai.AssistantMessage(m.Content)

			var toolcalls []openai.ChatCompletionMessageToolCallParam

			for _, t := range m.ToolCalls {
				toolcall := openai.ChatCompletionMessageToolCallParam{
					ID:   openai.F(t.ID),
					Type: openai.F(openai.ChatCompletionMessageToolCallTypeFunction),

					Function: openai.F(openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      openai.F(t.Name),
						Arguments: openai.F(t.Arguments),
					}),
				}

				toolcalls = append(toolcalls, toolcall)
			}

			if len(toolcalls) > 0 {
				message.ToolCalls = openai.F(toolcalls)
			}

			result = append(result, message)

		case provider.MessageRoleTool:
			message := openai.ToolMessage(m.Tool, m.Content)
			result = append(result, message)
		}
	}

	return result, nil
}

func convertTools(tools []provider.Tool) ([]openai.ChatCompletionToolParam, error) {
	var result []openai.ChatCompletionToolParam

	for _, t := range tools {
		if t.Name == "" {
			continue
		}

		function := openai.FunctionDefinitionParam{
			Name: openai.F(t.Name),

			Parameters: openai.F(openai.FunctionParameters(t.Parameters)),
		}

		if t.Description != "" {
			function.Description = openai.F(t.Description)
		}

		if t.Strict != nil {
			function.Strict = openai.F(*t.Strict)
		}

		tool := openai.ChatCompletionToolParam{
			Type: openai.F(openai.ChatCompletionToolTypeFunction),

			Function: openai.F(function),
		}

		result = append(result, tool)
	}

	return result, nil
}

func toDeltaToolCalls(calls []openai.ChatCompletionChunkChoicesDeltaToolCall) []provider.ToolCall {
	var result []provider.ToolCall

	for _, c := range calls {
		call := provider.ToolCall{
			ID: c.ID,

			Name:      c.Function.Name,
			Arguments: c.Function.Arguments,
		}

		result = append(result, call)
	}

	return result
}

func toToolCalls(calls []openai.ChatCompletionMessageToolCall) []provider.ToolCall {
	var result []provider.ToolCall

	for _, c := range calls {
		call := provider.ToolCall{
			ID: c.ID,

			Name:      c.Function.Name,
			Arguments: c.Function.Arguments,
		}

		result = append(result, call)
	}

	return result
}

func toDeltaCompletionResult(val openai.ChatCompletionChunkChoicesFinishReason) provider.CompletionReason {
	switch val {
	case openai.ChatCompletionChunkChoicesFinishReasonStop:
		return provider.CompletionReasonStop

	case openai.ChatCompletionChunkChoicesFinishReasonLength:
		return provider.CompletionReasonLength

	case openai.ChatCompletionChunkChoicesFinishReasonToolCalls:
		return provider.CompletionReasonTool

	case openai.ChatCompletionChunkChoicesFinishReasonContentFilter:
		return provider.CompletionReasonFilter

	default:
		return ""
	}
}

func toCompletionResult(val openai.ChatCompletionChoicesFinishReason) provider.CompletionReason {
	switch val {
	case openai.ChatCompletionChoicesFinishReasonStop:
		return provider.CompletionReasonStop

	case openai.ChatCompletionChoicesFinishReasonLength:
		return provider.CompletionReasonLength

	case openai.ChatCompletionChoicesFinishReasonToolCalls:
		return provider.CompletionReasonTool

	case openai.ChatCompletionChoicesFinishReasonContentFilter:
		return provider.CompletionReasonFilter

	default:
		return ""
	}
}
