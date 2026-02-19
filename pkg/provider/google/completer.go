package google

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"iter"
	"strings"

	"google.golang.org/genai"

	"github.com/adrianliechti/wingman/pkg/provider"
)

var _ provider.Completer = (*Completer)(nil)

type Completer struct {
	*Config
}

func NewCompleter(model string, options ...Option) (*Completer, error) {
	cfg := &Config{
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Completer{
		Config: cfg,
	}, nil
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if options == nil {
			options = new(provider.CompleteOptions)
		}

		client, err := c.newClient(ctx)

		if err != nil {
			yield(nil, err)
			return
		}

		contents, err := convertMessages(messages)

		if err != nil {
			yield(nil, err)
			return
		}

		config := convertGenerateConfig(convertInstruction(messages), options)

		iter := client.Models.GenerateContentStream(ctx, c.model, contents, config)

		for resp, err := range iter {
			if err != nil {
				yield(nil, convertError(err))
				return
			}

			delta := &provider.Completion{
				ID: resp.ResponseID,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,
				},

				Usage: toCompletionUsage(resp.UsageMetadata),
			}

			if len(resp.Candidates) > 0 {
				candidate := resp.Candidates[0]

				delta.Message.Content = toContent(candidate.Content)
			}

			if !yield(delta, nil) {
				return
			}
		}
	}
}

func convertInstruction(messages []provider.Message) *genai.Content {
	var parts []*genai.Part

	for _, m := range messages {
		if m.Role != provider.MessageRoleSystem {
			continue
		}

		for _, c := range m.Content {
			if c.Text != "" {
				parts = append(parts, genai.NewPartFromText(c.Text))
			}
		}
	}

	if len(parts) == 0 {
		return nil
	}

	return &genai.Content{
		Parts: parts,
	}
}

func convertGenerateConfig(instruction *genai.Content, options *provider.CompleteOptions) *genai.GenerateContentConfig {
	config := &genai.GenerateContentConfig{
		SystemInstruction: instruction,
	}

	// // Configure thinking based on effort level
	// switch options.Effort {
	// case provider.EffortMinimal:
	// 	config.ThinkingConfig = &genai.ThinkingConfig{
	// 		IncludeThoughts: true,
	// 		ThinkingLevel:   genai.ThinkingLevelMinimal,
	// 	}
	// case provider.EffortLow:
	// 	config.ThinkingConfig = &genai.ThinkingConfig{
	// 		IncludeThoughts: true,
	// 		ThinkingLevel:   genai.ThinkingLevelLow,
	// 	}
	// case provider.EffortMedium:
	// 	config.ThinkingConfig = &genai.ThinkingConfig{
	// 		IncludeThoughts: true,
	// 		ThinkingLevel:   genai.ThinkingLevelMedium,
	// 	}
	// case provider.EffortHigh:
	// 	config.ThinkingConfig = &genai.ThinkingConfig{
	// 		IncludeThoughts: true,
	// 		ThinkingLevel:   genai.ThinkingLevelHigh,
	// 	}
	// }

	if len(options.Tools) > 0 {
		config.Tools = convertTools(options.Tools)

		fcc := &genai.FunctionCallingConfig{}

		if options.ToolOptions != nil {
			switch options.ToolOptions.Choice {
			case provider.ToolChoiceNone:
				fcc.Mode = genai.FunctionCallingConfigModeNone

			case provider.ToolChoiceAuto:
				fcc.Mode = genai.FunctionCallingConfigModeAuto

			case provider.ToolChoiceAny:
				fcc.Mode = genai.FunctionCallingConfigModeAny
				fcc.AllowedFunctionNames = options.ToolOptions.Allowed
			}
		}

		// Upgrade to VALIDATED mode if any tool has Strict=true (schema enforcement)
		if fcc.Mode == "" || fcc.Mode == genai.FunctionCallingConfigModeAuto {
			for _, t := range options.Tools {
				if t.Strict != nil && *t.Strict {
					fcc.Mode = genai.FunctionCallingConfigModeValidated
					break
				}
			}
		}

		config.ToolConfig = &genai.ToolConfig{
			FunctionCallingConfig: fcc,
		}
	}

	if len(options.Stop) > 0 {
		config.StopSequences = options.Stop
	}

	if options.MaxTokens != nil {
		config.MaxOutputTokens = int32(*options.MaxTokens)
	}

	if options.Temperature != nil {
		config.Temperature = options.Temperature
	}

	if options.Schema != nil {
		config.ResponseMIMEType = "application/json"
		config.ResponseJsonSchema = options.Schema.Schema
	}

	return config
}

func convertContent(message provider.Message) (*genai.Content, error) {
	content := &genai.Content{}

	switch message.Role {
	case provider.MessageRoleUser:
		content.Role = "user"

		for _, c := range message.Content {
			if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
				part := genai.NewPartFromText(text)
				content.Parts = append(content.Parts, part)
			}

			if c.File != nil {
				switch c.File.ContentType {
				case "application/pdf", "image/png", "image/jpeg", "image/webp", "image/heic", "image/heif":
					part := genai.NewPartFromBytes(c.File.Content, c.File.ContentType)
					content.Parts = append(content.Parts, part)

				default:
					return nil, errors.New("unsupported content type")
				}
			}

			if c.ToolResult != nil {
				var data any
				var parameters map[string]any

				if err := json.Unmarshal([]byte(c.ToolResult.Data), &data); err == nil {
					if val, ok := data.(map[string]any); ok {
						parameters = val
					}

					if val, ok := data.([]any); ok {
						parameters = map[string]any{"data": val}
					}
				}

				if parameters == nil {
					parameters = map[string]any{"output": c.ToolResult.Data}
				}

				id, name, signature := parseToolID(c.ToolResult.ID)

				part := genai.NewPartFromFunctionResponse(name, parameters)
				part.FunctionResponse.ID = id
				part.ThoughtSignature = signature

				content.Parts = append(content.Parts, part)
			}
		}

	case provider.MessageRoleAssistant:
		content.Role = "model"

		for _, c := range message.Content {
			if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
				part := genai.NewPartFromText(text)
				content.Parts = append(content.Parts, part)
			}

			if c.Reasoning != nil {
				part := genai.NewPartFromText(c.Reasoning.Text)
				part.Thought = true
				part.ThoughtSignature = []byte(c.Reasoning.Signature)
				content.Parts = append(content.Parts, part)
			}

			if c.ToolCall != nil {
				var data map[string]any
				if err := json.Unmarshal([]byte(c.ToolCall.Arguments), &data); err != nil || data == nil {
					data = map[string]any{}
				}

				id, name, signature := parseToolID(c.ToolCall.ID)

				part := genai.NewPartFromFunctionCall(name, data)
				part.FunctionCall.ID = id
				part.ThoughtSignature = signature

				content.Parts = append(content.Parts, part)
			}
		}
	}

	return content, nil
}

func convertMessages(messages []provider.Message) ([]*genai.Content, error) {
	var result []*genai.Content

	for _, m := range messages {
		if m.Role == provider.MessageRoleUser {
			content, err := convertContent(m)

			if err != nil {
				return nil, err
			}

			result = append(result, content)
		}

		if m.Role == provider.MessageRoleAssistant {
			content, err := convertContent(m)

			if err != nil {
				return nil, err
			}

			result = append(result, content)
		}
	}

	return result, nil
}

func convertTools(tools []provider.Tool) []*genai.Tool {
	var functions []*genai.FunctionDeclaration

	for _, t := range tools {
		function := &genai.FunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,

			ParametersJsonSchema: t.Parameters,
		}

		functions = append(functions, function)
	}

	if len(functions) == 0 {
		return nil
	}

	return []*genai.Tool{
		{
			FunctionDeclarations: functions,
		},
	}
}

func toContent(content *genai.Content) []provider.Content {
	var parts []provider.Content

	for _, p := range content.Parts {
		// Handle thought/reasoning content
		if p.Thought && p.Text != "" {
			parts = append(parts, provider.ReasoningContent(provider.Reasoning{
				Text:      p.Text,
				Signature: string(p.ThoughtSignature),
			}))
			continue
		}

		if p.Text != "" {
			parts = append(parts, provider.TextContent(p.Text))
		}

		if p.FunctionCall != nil {
			data, _ := json.Marshal(p.FunctionCall.Args)

			call := provider.ToolCall{
				ID: formatToolID(p.FunctionCall.ID, p.FunctionCall.Name, p.ThoughtSignature),

				Name:      p.FunctionCall.Name,
				Arguments: string(data),
			}

			parts = append(parts, provider.ToolCallContent(call))
		}
	}

	return parts
}

func toCompletionUsage(metadata *genai.GenerateContentResponseUsageMetadata) *provider.Usage {
	if metadata == nil {
		return nil
	}

	return &provider.Usage{
		InputTokens:  int(metadata.PromptTokenCount),
		OutputTokens: int(metadata.CandidatesTokenCount),
	}
}

func formatToolID(id, name string, signature []byte) string {
	// Generate a unique ID if upstream didn't provide one
	if id == "" {
		id = generateCallID()
	}
	return strings.Join([]string{id, name, base64.StdEncoding.EncodeToString(signature)}, "::")
}

func generateCallID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func parseToolID(s string) (id, name string, signature []byte) {
	parts := strings.Split(s, "::")

	if len(parts) > 0 {
		id = parts[0]
	}

	if len(parts) > 1 {
		name = parts[1]
	}

	if len(parts) > 2 {
		signature, _ = base64.StdEncoding.DecodeString(parts[2])
	}

	return
}
