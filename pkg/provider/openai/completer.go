package openai

import (
	"context"
	"encoding/base64"
	"iter"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3"
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

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if options == nil {
			options = new(provider.CompleteOptions)
		}

		req, err := c.convertCompletionRequest(messages, options)

		if err != nil {
			yield(nil, err)
			return
		}

		stream := c.completions.NewStreaming(ctx, *req)

		for stream.Next() {
			chunk := stream.Current()

			delta := &provider.Completion{
				ID:    chunk.ID,
				Model: c.model,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,
				},

				Usage: toUsage(chunk.Usage),
			}

			if len(chunk.Choices) > 0 {
				choice := chunk.Choices[0]

				if choice.Delta.JSON.Content.Valid() {
					delta.Message.Content = append(delta.Message.Content, provider.TextContent(choice.Delta.Content))
				}

				if choice.Delta.JSON.Refusal.Valid() {
					delta.Message.Content = append(delta.Message.Content, provider.RefusalContent(choice.Delta.Refusal))
				}

				for _, c := range choice.Delta.ToolCalls {
					call := provider.ToolCall{
						ID: c.ID,

						Name:      c.Function.Name,
						Arguments: c.Function.Arguments,
					}

					delta.Message.Content = append(delta.Message.Content, provider.ToolCallContent(call))
				}

				delta.Status = toCompletionStatus(choice.FinishReason)
			}

			if !yield(delta, nil) {
				return
			}
		}

		if err := stream.Err(); err != nil {
			yield(nil, convertError(err))
			return
		}
	}
}

func (c *Completer) convertCompletionRequest(input []provider.Message, options *provider.CompleteOptions) (*openai.ChatCompletionNewParams, error) {
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

	if !strings.Contains(c.url, "api.mistral.ai") {
		req.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		}
	}

	if len(tools) > 0 {
		req.Tools = tools
	}

	if options.ToolOptions != nil {
		req.ToolChoice = convertToolChoice(options.ToolOptions)

		if options.ToolOptions.DisableParallelToolCalls {
			req.ParallelToolCalls = openai.Bool(false)
		}
	}

	if len(messages) > 0 {
		req.Messages = messages
	}

	if options.ReasoningOptions != nil && !slices.Contains(LegacyModels, c.model) {
		switch options.ReasoningOptions.Effort {
		case provider.EffortNone:
			req.ReasoningEffort = openai.ReasoningEffortNone

		case provider.EffortMinimal:
			req.ReasoningEffort = openai.ReasoningEffortMinimal

		case provider.EffortLow:
			req.ReasoningEffort = openai.ReasoningEffortLow

		case provider.EffortMedium:
			req.ReasoningEffort = openai.ReasoningEffortMedium

		case provider.EffortHigh:
			req.ReasoningEffort = openai.ReasoningEffortHigh

		case provider.EffortXHigh, provider.EffortMax:
			req.ReasoningEffort = openai.ReasoningEffortXhigh
		}
	}

	if options.OutputOptions != nil {
		switch options.OutputOptions.Verbosity {
		case provider.VerbosityLow:
			req.Verbosity = openai.ChatCompletionNewParamsVerbosityLow

		case provider.VerbosityMedium:
			req.Verbosity = openai.ChatCompletionNewParamsVerbosityMedium

		case provider.VerbosityHigh:
			req.Verbosity = openai.ChatCompletionNewParamsVerbosityHigh
		}
	}

	if options.Schema != nil {
		if options.Schema.Name == "json_object" {
			req.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
				OfJSONObject: &openai.ResponseFormatJSONObjectParam{},
			}
		} else {
			schemaData := options.Schema.Schema

			if options.Schema.Strict != nil && *options.Schema.Strict {
				schemaData = ensureAdditionalPropertiesFalse(schemaData)
			}

			schema := openai.ResponseFormatJSONSchemaJSONSchemaParam{
				Name:   options.Schema.Name,
				Schema: schemaData,
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
	}

	if options.Stop != nil {
		req.Stop = openai.ChatCompletionNewParamsStopUnion{
			OfStringArray: options.Stop,
		}
	}

	if options.MaxTokens != nil {
		if slices.Contains(LegacyModels, c.model) {
			req.MaxTokens = openai.Int(int64(*options.MaxTokens))
		} else {
			req.MaxCompletionTokens = openai.Int(int64(*options.MaxTokens))
		}
	}

	if options.Temperature != nil {
		if slices.Contains(LegacyModels, c.model) {
			req.Temperature = openai.Float(float64(*options.Temperature))
		}
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

			if !slices.Contains(LegacyModels, c.model) {
				message = openai.DeveloperMessage(parts)
			}

			result = append(result, message)

		case provider.MessageRoleUser:
			var parts []openai.ChatCompletionContentPartUnionParam
			var toolResults []*provider.ToolResult

			for _, c := range m.Content {
				if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
					parts = append(parts, openai.TextContentPart(text))
				}

				if c.File != nil {
					mime := c.File.ContentType
					content := base64.StdEncoding.EncodeToString(c.File.Content)

					switch {
					case mime == "image/png" || mime == "image/jpeg" || mime == "image/webp" || mime == "image/gif":
						image := openai.ChatCompletionContentPartImageImageURLParam{
							URL: "data:" + mime + ";base64," + content,
						}
						parts = append(parts, openai.ImageContentPart(image))

					case mime == "audio/wav" || mime == "audio/x-wav":
						audio := openai.ChatCompletionContentPartInputAudioInputAudioParam{
							Data:   content,
							Format: "wav",
						}
						parts = append(parts, openai.InputAudioContentPart(audio))

					case mime == "audio/mpeg" || mime == "audio/mp3":
						audio := openai.ChatCompletionContentPartInputAudioInputAudioParam{
							Data:   content,
							Format: "mp3",
						}
						parts = append(parts, openai.InputAudioContentPart(audio))

					default:
						// Forward as a generic file part — OpenAI's wire accepts any
						// mime here and decides what its models can interpret.
						file := openai.ChatCompletionContentPartFileFileParam{
							Filename: openai.String(c.File.Name),
							FileData: openai.String("data:" + mime + ";base64," + content),
						}
						parts = append(parts, openai.FileContentPart(file))
					}
				}

				if c.ToolResult != nil {
					toolResults = append(toolResults, c.ToolResult)
				}
			}

			// Each tool result becomes a separate tool message (OpenAI Chat Completions
			// format — text-only at the wire, so non-text parts are dropped here).
			for _, tr := range toolResults {
				var b strings.Builder
				for _, p := range tr.Parts {
					if p.Text != "" {
						b.WriteString(p.Text)
					}
				}
				result = append(result, openai.ToolMessage(b.String(), tr.ID))
			}

			if len(toolResults) == 0 {
				result = append(result, openai.UserMessage(parts))
			}

		case provider.MessageRoleAssistant:
			message := openai.ChatCompletionAssistantMessageParam{}

			var content []openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion

			for _, c := range m.Content {
				if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
					content = append(content, openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
						OfText: &openai.ChatCompletionContentPartTextParam{
							Text: text,
						},
					})
				}

				if c.ToolCall != nil {
					call := openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID: c.ToolCall.ID,

							Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
								Name:      c.ToolCall.Name,
								Arguments: c.ToolCall.Arguments,
							},
						},
					}

					message.ToolCalls = append(message.ToolCalls, call)
				}
			}

			if len(content) > 0 {
				message.Content.OfArrayOfContentParts = content
			}

			result = append(result, openai.ChatCompletionMessageParamUnion{OfAssistant: &message})
		}
	}

	return result, nil
}

func convertTools(tools []provider.Tool) ([]openai.ChatCompletionToolUnionParam, error) {
	var result []openai.ChatCompletionToolUnionParam

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

		tool := openai.ChatCompletionToolUnionParam{
			OfFunction: &openai.ChatCompletionFunctionToolParam{
				Function: function,
			},
		}

		result = append(result, tool)
	}

	return result, nil
}

func convertToolChoice(opts *provider.ToolOptions) openai.ChatCompletionToolChoiceOptionUnionParam {
	// Force a specific function when exactly one tool is required — universally supported format.
	if len(opts.Allowed) == 1 && opts.Choice == provider.ToolChoiceAny {
		return openai.ChatCompletionToolChoiceOptionUnionParam{
			OfFunctionToolChoice: &openai.ChatCompletionNamedToolChoiceParam{
				Function: openai.ChatCompletionNamedToolChoiceFunctionParam{
					Name: opts.Allowed[0],
				},
			},
		}
	}

	// For all other cases use the simple string mode. When multiple tools are in
	// the allowed list we can't restrict universally across OpenAI-compatible
	// providers, so we fall back to the plain required/auto/none mode.
	modes := map[provider.ToolChoice]string{
		provider.ToolChoiceNone: "none",
		provider.ToolChoiceAuto: "auto",
		provider.ToolChoiceAny:  "required",
	}

	return openai.ChatCompletionToolChoiceOptionUnionParam{
		OfAuto: openai.String(modes[opts.Choice]),
	}
}

// toCompletionStatus maps the upstream chat-completions finish_reason to a
// provider status. "stop" / "tool_calls" / "function_call" are normal terminations
// and stay as the zero value; only truncation and content-filter map to a
// non-default status so the wire-level finish_reason can be reconstructed
// downstream.
func toCompletionStatus(finishReason string) provider.CompletionStatus {
	switch finishReason {
	case "length":
		return provider.CompletionStatusIncomplete
	case "content_filter":
		return provider.CompletionStatusRefused
	}
	return ""
}

func toUsage(metadata openai.CompletionUsage) *provider.Usage {
	if metadata.TotalTokens == 0 && metadata.PromptTokensDetails.CachedTokens == 0 {
		return nil
	}

	return &provider.Usage{
		InputTokens:  int(metadata.PromptTokens),
		OutputTokens: int(metadata.CompletionTokens),

		CacheReadInputTokens: int(metadata.PromptTokensDetails.CachedTokens),
	}
}
