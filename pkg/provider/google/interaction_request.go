package google

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/computeruse"
	"github.com/adrianliechti/wingman/pkg/provider/tools/custom"
	"github.com/adrianliechti/wingman/pkg/provider/tools/shell"
	"github.com/adrianliechti/wingman/pkg/provider/tools/texteditor"
	"google.golang.org/genai/interactions/models/interactions"
)

// Google's documented placeholder permits replaying tool traces from other
// models that cannot provide Gemini thought signatures. Real signatures are
// always replayed unchanged. The Interactions API accepts it on thought steps.
const unsignedThoughtSignature = "skip_thought_signature_validator"

func convertInteraction(model string, messages []provider.Message, options *provider.CompleteOptions) (*interactions.CreateModelInteraction, error) {
	steps, err := convertMessages(messages)
	if err != nil {
		return nil, err
	}
	flatTools := provider.FlattenTools(options.Tools)
	tools, err := convertTools(flatTools)
	if err != nil {
		return nil, err
	}
	config := &interactions.GenerationConfig{
		MaxOutputTokens:   options.MaxTokens,
		StopSequences:     options.Stop,
		ThinkingSummaries: new(interactions.ThinkingSummariesNone),
	}
	if reasoning := options.ReasoningOptions; reasoning != nil {
		level, err := thinkingLevel(reasoning)
		if err != nil {
			return nil, err
		}
		config.ThinkingLevel = level
		if reasoning.IncludeSummary && reasoning.Type != provider.ReasoningTypeDisabled {
			config.ThinkingSummaries = new(interactions.ThinkingSummariesAuto)
		}
	}
	if len(tools) > 0 {
		choice := interactions.ToolChoiceTypeAuto
		if options.ToolOptions != nil && options.ToolOptions.Choice != "" {
			choice = interactions.ToolChoiceType(options.ToolOptions.Choice)
		}
		if choice == interactions.ToolChoiceTypeAuto {
			for _, tool := range flatTools {
				if tool.Strict != nil && *tool.Strict {
					choice = interactions.ToolChoiceTypeValidated
					break
				}
			}
		}
		selection := interactions.NewToolChoice(choice)
		if options.ToolOptions != nil && len(options.ToolOptions.Allowed) > 0 && choice == interactions.ToolChoiceTypeAny {
			selection = interactions.NewToolChoice(interactions.ToolChoiceConfig{
				AllowedTools: &interactions.AllowedTools{Mode: &choice, Tools: options.ToolOptions.Allowed},
			})
		}
		config.ToolChoice = &selection
	}
	request := &interactions.CreateModelInteraction{
		Model:            interactions.Model(strings.TrimPrefix(model, "models/")),
		Input:            new(interactions.NewInteractionsInput(steps)),
		GenerationConfig: config,
		Tools:            tools,
		Store:            new(false),
		Stream:           new(true),
		ServiceTier:      serviceTier(options.Tier),
	}
	var instructions []string
	for _, message := range messages {
		if message.Role == provider.MessageRoleSystem {
			for _, part := range message.Content {
				if part.Text != "" {
					instructions = append(instructions, part.Text)
				}
			}
		}
	}
	if len(instructions) > 0 {
		request.SystemInstruction = new(strings.Join(instructions, "\n\n"))
	}
	if options.Schema != nil {
		format := interactions.NewResponseFormat(interactions.TextResponseFormat{
			MimeType: new(interactions.TextResponseFormatMimeTypeApplicationJSON),
			Schema:   options.Schema.Properties,
		})
		request.ResponseFormat = new(interactions.NewCreateModelInteractionResponseFormat(format))
	}
	return request, nil
}

// Low is the common floor for Gemini text models: several models do not
// support minimal or disabling thinking. An omitted effort keeps the default.
func thinkingLevel(reasoning *provider.ReasoningOptions) (*interactions.ThinkingLevel, error) {
	if reasoning.Type == provider.ReasoningTypeDisabled {
		return new(interactions.ThinkingLevelLow), nil
	}
	switch reasoning.Effort {
	case "":
		return nil, nil
	case provider.EffortMinimal, provider.EffortLow:
		return new(interactions.ThinkingLevelLow), nil
	case provider.EffortMedium:
		return new(interactions.ThinkingLevelMedium), nil
	case provider.EffortHigh, provider.EffortXHigh, provider.EffortMax:
		return new(interactions.ThinkingLevelHigh), nil
	default:
		return nil, fmt.Errorf("gemini: unsupported reasoning effort %q", reasoning.Effort)
	}
}

// convertMessages preserves content order by flushing text/media at each
// thought or tool boundary. Tool results are independent execution steps.
func convertMessages(messages []provider.Message) ([]interactions.Step, error) {
	names := map[string]string{}
	for _, message := range messages {
		for _, part := range message.Content {
			if call := part.ToolCall; call != nil {
				id, _, _ := parseToolID(call.ID)
				names[id] = provider.FlattenToolName(*call)
			}
		}
	}
	steps := make([]interactions.Step, 0, len(messages))
	// Responses can split one assistant turn across several messages. A
	// signature covers its parallel calls until a user input or tool result.
	hasSignature := false
	for _, message := range messages {
		if message.Role != provider.MessageRoleUser && message.Role != provider.MessageRoleAssistant {
			continue
		}
		var content []interactions.Content
		flush := func() {
			if len(content) == 0 {
				return
			}
			if message.Role == provider.MessageRoleAssistant {
				// Thinking turns must begin with a thought, including when
				// imported text precedes a later summary or tool call.
				if !hasSignature {
					steps = append(steps, interactions.NewStep(interactions.ThoughtStep{Signature: new(unsignedThoughtSignature)}))
					hasSignature = true
				}
				steps = append(steps, interactions.NewStep(interactions.ModelOutputStep{Content: content}))
			} else {
				steps = append(steps, interactions.NewStep(interactions.UserInputStep{Content: content}))
				hasSignature = false
			}
			content = nil
		}
		for _, part := range message.Content {
			if part.Text != "" {
				content = append(content, interactions.NewContent(interactions.TextContent{Text: part.Text}))
			}
			if part.File != nil {
				media, err := fileContent(part.File)
				if err != nil {
					return nil, err
				}
				content = append(content, media)
			}
			if reasoning := part.Reasoning; reasoning != nil && message.Role == provider.MessageRoleAssistant {
				flush()
				thought := interactions.ThoughtStep{}
				if reasoning.Signature != "" {
					thought.Signature = new(reasoning.Signature)
				}
				parts := reasoning.Summary
				if reasoning.SummaryText() == "" {
					parts = reasoning.Content
				}
				for _, part := range parts {
					if part.Text == "" {
						continue
					}
					thought.Summary = append(thought.Summary, interactions.NewThoughtSummaryContent(interactions.TextContent{Text: part.Text}))
				}
				if thought.Signature != nil || len(thought.Summary) > 0 {
					if thought.Signature == nil {
						thought.Signature = new(unsignedThoughtSignature)
					}
					steps = append(steps, interactions.NewStep(thought))
					hasSignature = true
				}
			}
			if call := part.ToolCall; call != nil {
				flush()
				arguments := call.Arguments
				if call.Kind == provider.ToolKindCustom && !custom.IsWrapped(arguments) {
					arguments = custom.Wrap(arguments)
				}
				if arguments == "" {
					arguments = "{}"
				}
				data, err := toolArguments(arguments)
				if err != nil {
					return nil, fmt.Errorf("gemini: tool call %q arguments must be a JSON object", call.ID)
				}
				id, encodedName, signature := parseToolID(call.ID)
				name := provider.FlattenToolName(*call)
				if name == "" {
					name = encodedName
				}
				// Older Wingman histories put the signature on the tool ID.
				// Interactions represents it as a preceding thought step.
				if signature != "" {
					steps = append(steps, interactions.NewStep(interactions.ThoughtStep{Signature: &signature}))
					hasSignature = true
				}
				if !hasSignature {
					steps = append(steps, interactions.NewStep(interactions.ThoughtStep{Signature: new(unsignedThoughtSignature)}))
					hasSignature = true
				}
				steps = append(steps, interactions.NewStep(interactions.FunctionCallStep{ID: id, Name: name, Arguments: data}))
			}
			if result := part.ToolResult; result != nil {
				flush()
				id, name, _ := parseToolID(result.ID)
				if name == "" {
					name = names[id]
				}
				parts := make([]interactions.FunctionResultSubcontent, 0, len(result.Parts))
				var text strings.Builder
				var hasImage bool
				for _, part := range result.Parts {
					if part.Text != "" {
						text.WriteString(part.Text)
						parts = append(parts, interactions.NewFunctionResultSubcontent(interactions.TextContent{Text: part.Text}))
					}
					if part.File != nil {
						media, err := fileContent(part.File)
						if err != nil {
							return nil, err
						}
						switch {
						case media.ImageContent != nil:
							hasImage = true
							parts = append(parts, interactions.NewFunctionResultSubcontent(*media.ImageContent))
						case media.TextContent != nil:
							text.WriteString(media.TextContent.Text)
							parts = append(parts, interactions.NewFunctionResultSubcontent(*media.TextContent))
						default:
							return nil, fmt.Errorf("gemini: tool results do not support %q files", part.File.ContentType)
						}
					}
				}
				// A text result is a string. Content arrays are multimodal,
				// which older models reject even if every element is text.
				output := interactions.NewFunctionResultStepResultUnion(text.String())
				if hasImage {
					output = interactions.NewFunctionResultStepResultUnion(parts)
				}
				response := interactions.FunctionResultStep{
					CallID: id, IsError: new(result.IsError),
					Result: output,
				}
				if name != "" {
					response.Name = &name
				}
				steps = append(steps, interactions.NewStep(response))
				hasSignature = false
			}
		}
		flush()
	}
	return steps, nil
}

func toolArguments(arguments string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(arguments))
	// Preserve large integer IDs and precise decimals when replaying calls.
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("expected a JSON object")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing JSON")
	}
	return object, nil
}

func fileContent(file *provider.File) (interactions.Content, error) {
	mediaType, _, err := mime.ParseMediaType(file.ContentType)
	if err != nil {
		return interactions.Content{}, fmt.Errorf("gemini: invalid file content type %q", file.ContentType)
	}
	data := new(base64.StdEncoding.EncodeToString(file.Content))
	var uri *string
	// Generated URI-only media is kept in File.Content by the shared provider
	// convention. Replay the reference rather than base64-encoding the URL.
	reference := string(file.Content)
	if strings.HasPrefix(reference, "https://") || strings.HasPrefix(reference, "http://") || strings.HasPrefix(reference, "gs://") {
		uri, data = &reference, nil
	}
	switch {
	case strings.HasPrefix(mediaType, "image/"):
		return interactions.NewContent(interactions.ImageContent{Data: data, URI: uri, MimeType: new(interactions.ImageContentMimeType(mediaType))}), nil
	case strings.HasPrefix(mediaType, "audio/"):
		return interactions.NewContent(interactions.AudioContent{Data: data, URI: uri, MimeType: new(interactions.AudioContentMimeType(mediaType))}), nil
	case strings.HasPrefix(mediaType, "video/"):
		return interactions.NewContent(interactions.VideoContent{Data: data, URI: uri, MimeType: new(interactions.VideoContentMimeType(mediaType))}), nil
	case mediaType == "application/pdf" || mediaType == "text/csv":
		return interactions.NewContent(interactions.DocumentContent{Data: data, URI: uri, MimeType: new(interactions.DocumentContentMimeType(mediaType))}), nil
	case strings.HasPrefix(mediaType, "text/") || mediaType == "application/json":
		return interactions.NewContent(interactions.TextContent{Text: string(file.Content)}), nil
	default:
		return interactions.Content{}, fmt.Errorf("gemini: unsupported file content type %q", file.ContentType)
	}
}

func convertTools(tools []provider.Tool) ([]interactions.Tool, error) {
	var functions []interactions.Tool
	for _, tool := range tools {
		switch tool.Kind {
		case provider.ToolKindTextEditor:
			tool = texteditor.FunctionTool(tool)
		case provider.ToolKindComputer:
			tool = computeruse.FunctionTool(tool)
		case provider.ToolKindShell:
			tool = shell.FunctionTool(tool)
		case provider.ToolKindCustom:
			tool = custom.FunctionTool(tool)
		}
		if tool.Kind != provider.ToolKindFunction {
			return nil, provider.UnsupportedToolError(tool)
		}
		functions = append(functions, interactions.NewTool(interactions.Function{
			Name: new(tool.Name), Description: new(tool.Description), Parameters: tool.Parameters,
		}))
	}
	return functions, nil
}
