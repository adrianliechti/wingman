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
	"github.com/openai/openai-go/shared"
)

var _ provider.Completer = (*Completer)(nil)

type Completer struct {
	*Config
	completions openai.ChatCompletionService
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

	content := provider.MessageContent{}

	if choice.Message.JSON.Content.IsPresent() {
		content = append(content, provider.TextContent(choice.Message.Content))
	}

	return &provider.Completion{
		ID:     completion.ID,
		Reason: reason,

		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: content,

			ToolCalls: fromToolCalls(choice.Message.ToolCalls),
		},

		Usage: toUsage(completion.Usage),
	}, nil
}

func (c *Completer) completeStream(ctx context.Context, req openai.ChatCompletionNewParams, options *provider.CompleteOptions) (*provider.Completion, error) {
	stream := c.completions.NewStreaming(ctx, req)

	result := provider.CompletionAccumulator{}

	for stream.Next() {
		chunk := stream.Current()

		delta := provider.Completion{
			ID: chunk.ID,

			Message: &provider.Message{
				Role: provider.MessageRoleAssistant,
			},

			Usage: toUsage(chunk.Usage),
		}

		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]

			delta.Reason = toCompletionResult(choice.FinishReason)

			if choice.Delta.JSON.Content.IsPresent() {
				delta.Message.Content = append(delta.Message.Content, provider.TextContent(choice.Delta.Content))
			}

			if choice.Delta.JSON.Refusal.IsPresent() {
				delta.Message.Content = append(delta.Message.Content, provider.TextContent(choice.Delta.Refusal))
			}

			delta.Message.ToolCalls = fromChunkToolCalls(choice.Delta.ToolCalls)
		}

		result.Add(delta)

		if err := options.Stream(ctx, delta); err != nil {
			return nil, err
		}
	}

	if err := stream.Err(); err != nil {
		return nil, convertError(err)
	}

	return result.Result(), nil
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
		Model: c.model,
	}

	if options.Stream != nil {
		if !strings.Contains(c.url, "api.mistral.ai") {
			req.StreamOptions = openai.ChatCompletionStreamOptionsParam{
				IncludeUsage: openai.Bool(true),
			}
		}
	}

	if len(tools) > 0 {
		req.Tools = tools
	}

	if len(messages) > 0 {
		req.Messages = messages
	}

	switch options.Effort {
	case provider.ReasoningEffortLow:
		req.ReasoningEffort = shared.ReasoningEffortLow

	case provider.ReasoningEffortMedium:
		req.ReasoningEffort = shared.ReasoningEffortMedium

	case provider.ReasoningEffortHigh:
		req.ReasoningEffort = shared.ReasoningEffortHigh
	}

	if options.Format == provider.CompletionFormatJSON {
		req.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &openai.ResponseFormatJSONObjectParam{},
		}
	}

	if options.Schema != nil {
		schema := openai.ResponseFormatJSONSchemaJSONSchemaParam{
			Name:   options.Schema.Name,
			Schema: options.Schema.Schema,
		}

		if options.Schema.Description != "" {
			schema.Description = openai.String(options.Schema.Description)
		}

		if options.Schema.Strict != nil {
			schema.Strict = openai.Bool(*options.Schema.Strict)
		}

		req.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
				JSONSchema: schema,
			},
		}
	}

	if options.Stop != nil {
		req.Stop = openai.ChatCompletionNewParamsStopUnion{
			OfChatCompletionNewsStopArray: options.Stop,
		}
	}

	if options.MaxTokens != nil {
		if slices.Contains([]string{"o1", "o1-mini", "o3-mini"}, c.model) {
			req.MaxCompletionTokens = openai.Int(int64(*options.MaxTokens))
		} else {
			req.MaxTokens = openai.Int(int64(*options.MaxTokens))
		}
	}

	if options.Temperature != nil {
		req.Temperature = openai.Float(float64(*options.Temperature))
	}

	return req, nil
}

func (c *Completer) convertMessages(input []provider.Message) ([]openai.ChatCompletionMessageParamUnion, error) {
	var result []openai.ChatCompletionMessageParamUnion

	for _, m := range input {
		switch m.Role {
		case provider.MessageRoleSystem:
			parts := []openai.ChatCompletionContentPartTextParam{}

			for _, c := range m.Content {
				if c.Text != "" {
					parts = append(parts, openai.ChatCompletionContentPartTextParam{Text: c.Text})
				}
			}

			message := openai.SystemMessage(parts)

			if slices.Contains([]string{"o1", "o1-mini", "o3-mini"}, c.model) {
				message = openai.DeveloperMessage(parts)
			}

			result = append(result, message)

		case provider.MessageRoleUser:
			parts := []openai.ChatCompletionContentPartUnionParam{}

			for _, c := range m.Content {
				if c.Text != "" {
					parts = append(parts, openai.TextContentPart(c.Text))
				}

				if c.File != nil {
					data, err := io.ReadAll(c.File.Content)

					if err != nil {
						return nil, err
					}

					mime := c.File.ContentType
					content := base64.StdEncoding.EncodeToString(data)

					switch c.File.ContentType {
					case "image/png", "image/jpeg", "image/webp", "image/gif":
						imageURL := openai.ChatCompletionContentPartImageImageURLParam{
							URL: "data:" + mime + ";base64," + content,
						}

						part := openai.ImageContentPart(imageURL)
						parts = append(parts, part)

					default:
						return nil, errors.New("unsupported content type")
					}
				}
			}

			result = append(result, openai.UserMessage(parts))

		case provider.MessageRoleAssistant:
			message := openai.ChatCompletionAssistantMessageParam{}

			var content []openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion

			for _, c := range m.Content {
				if c.Text != "" {
					content = append(content, openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
						OfText: &openai.ChatCompletionContentPartTextParam{
							Text: c.Text,
						},
					})
				}

				if c.Refusal != "" {
					content = append(content, openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
						OfRefusal: &openai.ChatCompletionContentPartRefusalParam{
							Refusal: c.Refusal,
						},
					})
				}
			}

			if len(content) > 0 {
				message.Content.OfArrayOfContentParts = content
			}

			for _, t := range m.ToolCalls {
				toolcall := openai.ChatCompletionMessageToolCallParam{
					ID: t.ID,

					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      t.Name,
						Arguments: t.Arguments,
					},
				}

				message.ToolCalls = append(message.ToolCalls, toolcall)
			}

			result = append(result, openai.ChatCompletionMessageParamUnion{OfAssistant: &message})

		case provider.MessageRoleTool:
			message := openai.ToolMessage(m.Content.Text(), m.Tool)
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
			Name: t.Name,

			Parameters: openai.FunctionParameters(t.Parameters),
		}

		if t.Description != "" {
			function.Description = openai.String(t.Description)
		}

		if t.Strict != nil {
			function.Strict = openai.Bool(*t.Strict)
		}

		tool := openai.ChatCompletionToolParam{
			Function: function,
		}

		result = append(result, tool)
	}

	return result, nil
}

func fromToolCalls(calls []openai.ChatCompletionMessageToolCall) []provider.ToolCall {
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

func fromChunkToolCalls(calls []openai.ChatCompletionChunkChoiceDeltaToolCall) []provider.ToolCall {
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

func toCompletionResult(val string) provider.CompletionReason {
	switch val {
	case "stop":
		return provider.CompletionReasonStop

	case "length":
		return provider.CompletionReasonLength

	case "tool_calls":
		return provider.CompletionReasonTool

	case "content_filter":
		return provider.CompletionReasonFilter

	default:
		return ""
	}
}

func toUsage(metadata openai.CompletionUsage) *provider.Usage {
	if metadata.TotalTokens == 0 {
		return nil
	}

	return &provider.Usage{
		InputTokens:  int(metadata.PromptTokens),
		OutputTokens: int(metadata.CompletionTokens),
	}
}
