package google

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"iter"
	"strings"

	"google.golang.org/genai"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/computeruse"
	"github.com/adrianliechti/wingman/pkg/provider/tools/shell"
	"github.com/adrianliechti/wingman/pkg/provider/tools/texteditor"
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

		toolAliases := provider.ToolAliases(options.Tools)

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

				delta.Message.Content = toContent(candidate.Content, toolAliases)
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

	if reasoning := options.ReasoningOptions; reasoning != nil {
		config.ThinkingConfig = &genai.ThinkingConfig{
			IncludeThoughts: reasoning.IncludeSummary,
		}

		if reasoning.Type == provider.ReasoningTypeDisabled {
			// Gemini models cannot fully disable thinking; minimal is the
			// closest equivalent to "no thinking".
			config.ThinkingConfig.IncludeThoughts = false
			config.ThinkingConfig.ThinkingLevel = genai.ThinkingLevelMinimal
		} else {
			switch reasoning.Effort {
			case provider.EffortMinimal:
				config.ThinkingConfig.ThinkingLevel = genai.ThinkingLevelMinimal
			case provider.EffortLow:
				config.ThinkingConfig.ThinkingLevel = genai.ThinkingLevelLow
			case provider.EffortMedium:
				config.ThinkingConfig.ThinkingLevel = genai.ThinkingLevelMedium
			case provider.EffortHigh, provider.EffortXHigh, provider.EffortMax:
				config.ThinkingConfig.ThinkingLevel = genai.ThinkingLevelHigh
			}
		}
	}

	flatTools := provider.FlattenTools(options.Tools)

	if len(flatTools) > 0 {
		config.Tools = convertTools(flatTools)

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

		if fcc.Mode == "" || fcc.Mode == genai.FunctionCallingConfigModeAuto {
			for _, t := range flatTools {
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
		config.ResponseJsonSchema = options.Schema.Properties
	}

	return config
}

func convertContent(message provider.Message, toolCallNames map[string]string) (*genai.Content, error) {
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
				// Gemini's inline_data accepts any mime; the model decides
				// what it can interpret. Forward as-is.
				part := genai.NewPartFromBytes(c.File.Content, c.File.ContentType)
				content.Parts = append(content.Parts, part)
			}

			if c.ToolResult != nil {
				var (
					textBuilder strings.Builder
					fileParts   []*genai.FunctionResponsePart
				)
				for _, p := range c.ToolResult.Parts {
					if p.Text != "" {
						textBuilder.WriteString(p.Text)
					}
					if p.File != nil {
						fileParts = append(fileParts, &genai.FunctionResponsePart{
							InlineData: &genai.FunctionResponseBlob{
								MIMEType:    p.File.ContentType,
								Data:        p.File.Content,
								DisplayName: p.File.Name,
							},
						})
					}
				}
				text := textBuilder.String()

				parameters := map[string]any{}

				if text != "" {
					var data any
					if err := json.Unmarshal([]byte(text), &data); err == nil {
						switch val := data.(type) {
						case map[string]any:
							parameters = val
						case []any:
							parameters = map[string]any{"data": val}
						default:
							parameters = map[string]any{"output": text}
						}
					} else {
						parameters = map[string]any{"output": text}
					}
				}

				id, encodedName, signature := parseToolID(c.ToolResult.ID)

				// Resolve the tool name: prefer the encoded round-trip form
				// (assistant calls Gemini originated), fall back to looking up
				// the matching prior tool call's Name by id. Gemini's
				// FunctionResponse requires a non-empty name on the wire.
				name := encodedName
				if name == "" {
					name = toolCallNames[id]
				}

				part := genai.NewPartFromFunctionResponse(name, parameters)
				part.FunctionResponse.ID = id
				part.FunctionResponse.Parts = fileParts
				part.ThoughtSignature = signature

				content.Parts = append(content.Parts, part)
			}
		}

	case provider.MessageRoleAssistant:
		content.Role = "model"

		var pendingSig []byte

		for _, c := range message.Content {
			if c.Reasoning != nil {
				if c.Reasoning.Text == "" && c.Reasoning.Summary == "" {
					if c.Reasoning.Signature != "" {
						pendingSig = []byte(c.Reasoning.Signature)
					}
					continue
				}

				text := c.Reasoning.Text
				if text == "" {
					text = c.Reasoning.Summary
				}

				part := genai.NewPartFromText(text)
				part.Thought = true
				if c.Reasoning.Signature != "" {
					part.ThoughtSignature = []byte(c.Reasoning.Signature)
				}
				content.Parts = append(content.Parts, part)
				continue
			}

			if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
				part := genai.NewPartFromText(text)
				if pendingSig != nil {
					part.ThoughtSignature = pendingSig
					pendingSig = nil
				}
				content.Parts = append(content.Parts, part)
				continue
			}

			if c.ToolCall != nil {
				var data map[string]any
				if err := json.Unmarshal([]byte(c.ToolCall.Arguments), &data); err != nil || data == nil {
					data = map[string]any{}
				}

				id, encodedName, signature := parseToolID(c.ToolCall.ID)

				// Prefer the explicit Name field (always set on assistant tool
				// calls); fall back to the encoded suffix for round-tripped IDs.
				name := provider.FlattenToolName(*c.ToolCall)
				if name == "" {
					name = encodedName
				}

				part := genai.NewPartFromFunctionCall(name, data)
				part.FunctionCall.ID = id
				if signature != nil {
					part.ThoughtSignature = signature
				} else if pendingSig != nil {
					part.ThoughtSignature = pendingSig
					pendingSig = nil
				} else {
					part.ThoughtSignature = dummyThoughtSignature
				}

				content.Parts = append(content.Parts, part)
			}
		}

		if pendingSig != nil {
			part := &genai.Part{ThoughtSignature: pendingSig}
			content.Parts = append(content.Parts, part)
		}
	}

	return content, nil
}

func convertMessages(messages []provider.Message) ([]*genai.Content, error) {
	// Build a callID → name index from assistant tool calls so tool results
	// (which have no Name field) can recover the tool name when the id isn't
	// in encoded form. The lookup uses both the raw id and the plain id
	// component of an encoded id, since clients may replay either shape.
	toolCallNames := map[string]string{}
	for _, m := range messages {
		if m.Role != provider.MessageRoleAssistant {
			continue
		}
		for _, c := range m.Content {
			if c.ToolCall == nil || c.ToolCall.Name == "" {
				continue
			}
			name := provider.FlattenToolName(*c.ToolCall)

			toolCallNames[c.ToolCall.ID] = name
			if plain, _, _ := parseToolID(c.ToolCall.ID); plain != "" && plain != c.ToolCall.ID {
				toolCallNames[plain] = name
			}
		}
	}

	var result []*genai.Content
	for _, m := range messages {
		if m.Role == provider.MessageRoleUser || m.Role == provider.MessageRoleAssistant {
			content, err := convertContent(m, toolCallNames)
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
		if t.Kind == provider.ToolKindTextEditor {
			t = texteditor.FunctionTool(t)
		}

		if t.Kind == provider.ToolKindComputer {
			t = computeruse.FunctionTool(t)
		}

		if t.Kind == provider.ToolKindShell {
			t = shell.FunctionTool(t)
		}

		if t.Kind != provider.ToolKindFunction {
			continue
		}

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

func toContent(content *genai.Content, toolAliases map[string]provider.Tool) []provider.Content {
	var parts []provider.Content

	for _, p := range content.Parts {
		sig := string(p.ThoughtSignature)

		if p.Thought {
			parts = append(parts, provider.ReasoningContent(provider.Reasoning{
				Text:      p.Text,
				Signature: sig,
			}))
			continue
		}

		if sig != "" && p.FunctionCall == nil {
			parts = append(parts, provider.ReasoningContent(provider.Reasoning{
				ID:        "gemsig_" + generateCallID(),
				Signature: sig,
			}))
		}

		if p.Text != "" {
			parts = append(parts, provider.TextContent(p.Text))
		}

		// Inline media bytes — image-emitting Gemini models (gemini-*-image
		// preview families) return generated images this way.
		if p.InlineData != nil {
			parts = append(parts, provider.FileContent(&provider.File{
				Name:        p.InlineData.DisplayName,
				Content:     p.InlineData.Data,
				ContentType: p.InlineData.MIMEType,
			}))
		}

		// URI-based file reference (e.g. files uploaded via the Files API).
		// The URI is the only thing the upstream stored; pack it into the
		// File.Content per the codebase convention for URI-only references.
		if p.FileData != nil {
			parts = append(parts, provider.FileContent(&provider.File{
				Name:        p.FileData.DisplayName,
				Content:     []byte(p.FileData.FileURI),
				ContentType: p.FileData.MIMEType,
			}))
		}

		if p.FunctionCall != nil {
			data, _ := json.Marshal(p.FunctionCall.Args)

			call := provider.UnflattenToolCall(toolAliases, provider.ToolCall{
				ID: formatToolID(p.FunctionCall.ID, p.FunctionCall.Name, p.ThoughtSignature),

				Name:      p.FunctionCall.Name,
				Arguments: string(data),
			})

			parts = append(parts, provider.ToolCallContent(call))
		}
	}

	return parts
}

func toCompletionUsage(metadata *genai.GenerateContentResponseUsageMetadata) *provider.Usage {
	if metadata == nil {
		return nil
	}

	// Gemini's PromptTokenCount already includes cached tokens, and
	// thoughts tokens are reported separately from candidates. Add thoughts
	// to OutputTokens so callers see the full generated-token cost.
	return &provider.Usage{
		InputTokens:  int(metadata.PromptTokenCount),
		OutputTokens: int(metadata.CandidatesTokenCount) + int(metadata.ThoughtsTokenCount),

		CacheReadInputTokens: int(metadata.CachedContentTokenCount),
	}
}

// formatToolID packs a call id, tool name, and an optional thought signature
// into a single string of the form "id::name::base64sig" so that Gemini-served
// assistant tool calls can round-trip the name back to us when the client
// replays them — Gemini's FunctionResponse requires the name on the wire.
// A blank id is replaced with a freshly-generated one.
func formatToolID(id, name string, signature []byte) string {
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

func StripToolIDSignature(s string) string {
	id, name, signature := parseToolID(s)

	if signature == nil {
		return s
	}

	return id + "::" + name
}

// dummyThoughtSignature bypasses thought-signature validation for tool calls
// migrated from another model or provider.
// https://ai.google.dev/gemini-api/docs/thought-signatures#faqs
var dummyThoughtSignature = []byte("skip_thought_signature_validator")

// parseToolID is the inverse of formatToolID. For plain IDs without "::" the
// entire string is returned as id with empty name and signature.
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
