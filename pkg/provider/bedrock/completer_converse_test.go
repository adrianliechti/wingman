package bedrock

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// TestConvertConverseInput_EffortStaysInAdditionalFields pins the effort to
// additionalModelRequestFields: Claude 4.6 on Bedrock rejects the typed
// outputConfig.effort field but accepts output_config.effort there.
func TestConvertConverseInput_EffortStaysInAdditionalFields(t *testing.T) {
	c := &Completer{Config: &Config{model: "eu.anthropic.claude-opus-4-6-v1"}}

	req, err := c.convertConverseInput([]provider.Message{provider.UserMessage("hi")}, &provider.CompleteOptions{
		ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, Effort: provider.EffortLow},
	})
	if err != nil {
		t.Fatal(err)
	}

	if req.OutputConfig != nil {
		t.Fatalf("effort must not use the typed field: %+v", req.OutputConfig)
	}
	if req.AdditionalModelRequestFields == nil {
		t.Fatal("expected additional fields")
	}
	data, err := req.AdditionalModelRequestFields.MarshalSmithyDocument()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"output_config":{"effort":"low"}`) || !strings.Contains(string(data), "adaptive") {
		t.Fatalf("additional fields = %s", data)
	}
}

// TestConvertConverseInput_NativeOutputFormat verifies models with structured
// outputs take the schema as outputConfig.textFormat: the schema is reduced to
// the grammar subset, closed with additionalProperties: false, and no forced
// tool is added.
func TestConvertConverseInput_NativeOutputFormat(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-sonnet-4-6"}}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{"type": "string", "minLength": float64(1)},
		},
		"required": []string{"title"},
	}

	req, err := c.convertConverseInput([]provider.Message{provider.UserMessage("Return JSON.")}, &provider.CompleteOptions{
		Schema: &provider.Schema{Name: "classify_chat", Description: "Classify the chat", Properties: schema},
	})
	if err != nil {
		t.Fatal(err)
	}

	if req.ToolConfig != nil {
		t.Fatalf("schema must not become a forced tool: %+v", req.ToolConfig)
	}
	if req.OutputConfig == nil || req.OutputConfig.TextFormat == nil {
		t.Fatalf("missing text format: %+v", req.OutputConfig)
	}

	format := req.OutputConfig.TextFormat
	if format.Type != types.OutputFormatTypeJsonSchema {
		t.Fatalf("format type = %q", format.Type)
	}
	structure, ok := format.Structure.(*types.OutputFormatStructureMemberJsonSchema)
	if !ok {
		t.Fatalf("structure = %T", format.Structure)
	}
	if aws.ToString(structure.Value.Name) != "classify_chat" || aws.ToString(structure.Value.Description) != "Classify the chat" {
		t.Fatalf("definition = %+v", structure.Value)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(aws.ToString(structure.Value.Schema)), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["additionalProperties"] != false {
		t.Fatalf("schema not closed: %v", decoded)
	}
	title := decoded["properties"].(map[string]any)["title"].(map[string]any)
	if _, ok := title["minLength"]; ok {
		t.Fatalf("unsupported constraint reached the grammar: %v", title)
	}
	if _, ok := schema["additionalProperties"]; ok {
		t.Fatal("input schema mutated")
	}
}

// TestConvertConverseInput_SchemaWithoutPropertiesForcesTool verifies JSON
// mode without a schema keeps the forced-tool emulation even on models with
// native output formats.
func TestConvertConverseInput_SchemaWithoutPropertiesForcesTool(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-sonnet-4-6"}}

	req, err := c.convertConverseInput([]provider.Message{provider.UserMessage("Return JSON.")}, &provider.CompleteOptions{
		Schema: &provider.Schema{Name: "json"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if req.OutputConfig != nil {
		t.Fatalf("unexpected output config: %+v", req.OutputConfig)
	}
	if req.ToolConfig == nil || len(req.ToolConfig.Tools) == 0 {
		t.Fatal("expected forced schema tool")
	}
}

// TestConverseAdditionalFields_NativeSchemaKeepsThinking verifies native
// output formats do not force thinking off the way a forced tool call does.
func TestConverseAdditionalFields_NativeSchemaKeepsThinking(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-sonnet-4-6"}}

	fields, thinking := c.converseAdditionalFields(&provider.CompleteOptions{
		Schema:           &provider.Schema{Name: "classify", Properties: testSchema},
		ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive},
	})

	if !thinking.Enabled {
		t.Fatal("expected thinking enabled with native output format")
	}
	got, _ := fields["thinking"].(map[string]any)
	if got["type"] != "adaptive" {
		t.Fatalf("thinking: got %v, want adaptive", fields["thinking"])
	}
}

func systemTexts(blocks []types.SystemContentBlock) []string {
	var texts []string
	for _, block := range blocks {
		if text, ok := block.(*types.SystemContentBlockMemberText); ok {
			texts = append(texts, text.Value)
		}
	}
	return texts
}

func messageRoles(messages []types.Message) []types.ConversationRole {
	roles := make([]types.ConversationRole, 0, len(messages))
	for _, message := range messages {
		roles = append(roles, message.Role)
	}
	return roles
}

// Converse requires system instructions in the top-level field, even for
// models that accept mid-conversation system messages through InvokeModel.
func TestConvertConverseInput_MidConversationSystem(t *testing.T) {
	history := []provider.Message{
		provider.SystemMessage("Be brief."),
		provider.UserMessage("Start"),
		{Role: provider.MessageRoleSystem, Content: []provider.Content{
			provider.InstructionsContent(provider.Instructions{Text: "Answer in German.", Scope: provider.InstructionScopeConversation}),
			provider.InstructionsContent(provider.Instructions{Text: "Expired instruction.", Scope: provider.InstructionScopeTurn}),
		}},
		provider.AssistantMessage("Erste Antwort"),
		provider.UserMessage("Continue"),
		{Role: provider.MessageRoleSystem, Content: []provider.Content{
			provider.InstructionsContent(provider.Instructions{Text: "Keep this answer short.", Scope: provider.InstructionScopeTurn}),
		}},
	}

	for _, model := range []string{"eu.anthropic.claude-opus-5", "eu.anthropic.claude-opus-4-8", "eu.anthropic.claude-fable-5", "anthropic.claude-sonnet-4-6"} {
		t.Run(model, func(t *testing.T) {
			c := &Completer{Config: &Config{model: model}}
			req, err := c.convertConverseInput(history, &provider.CompleteOptions{})
			if err != nil {
				t.Fatal(err)
			}
			wantRoles := []types.ConversationRole{types.ConversationRoleUser, types.ConversationRoleAssistant, types.ConversationRoleUser}
			if got := messageRoles(req.Messages); !slices.Equal(got, wantRoles) {
				t.Fatalf("roles = %v, want %v", got, wantRoles)
			}
			wantSystem := []string{"Be brief.", "Answer in German.", "Keep this answer short."}
			if got := systemTexts(req.System); !slices.Equal(got, wantSystem) {
				t.Fatalf("system = %v, want %v", got, wantSystem)
			}
		})
	}
}

// Without a client limit Converse caps Claude Opus 5.5 at 4096 output tokens,
// which truncates tool calls after thinking and a preamble. Claude models get
// their own limit instead, except context-bound ones, where it would fail long
// prompts; the client's value still wins. Want 0 means no limit is sent.
func TestConvertConverseInput_DefaultMaxTokens(t *testing.T) {
	limit := 2048
	profile := "arn:aws:bedrock:eu-central-1:123456789012:application-inference-profile/r7rhmi9b01o7"

	cases := []struct {
		model   string
		options *provider.CompleteOptions
		want    int32
	}{
		{"eu.anthropic.claude-opus-5-5", &provider.CompleteOptions{}, 128000},
		{"eu.anthropic.claude-sonnet-5-5", &provider.CompleteOptions{}, 128000},
		{"anthropic.claude-haiku-5-5", &provider.CompleteOptions{}, 128000},
		{"eu.anthropic.claude-sonnet-4-6", &provider.CompleteOptions{}, 128000},
		{"eu.anthropic.claude-haiku-4-5-20251001-v1:0", &provider.CompleteOptions{}, 64000},
		{"eu.anthropic.claude-sonnet-4-20250514-v1:0", &provider.CompleteOptions{}, 0},
		{"us.anthropic.claude-opus-4-1-20250805-v1:0", &provider.CompleteOptions{}, 0},
		{"anthropic.claude-3-5-haiku-20241022-v1:0", &provider.CompleteOptions{}, 0},
		{"amazon.nova-pro-v1:0", &provider.CompleteOptions{}, 0},
		{"eu.anthropic.claude-sonnet-5", &provider.CompleteOptions{MaxTokens: &limit}, 2048},
		{profile, &provider.CompleteOptions{}, 0},
		{"arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-sonnet-5", &provider.CompleteOptions{}, 128000},
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			c := &Completer{Config: &Config{model: tc.model}}
			req, err := c.convertConverseInput([]provider.Message{provider.UserMessage("hi")}, tc.options)
			if err != nil {
				t.Fatal(err)
			}
			if got := req.InferenceConfig.MaxTokens; (got == nil) != (tc.want == 0) || aws.ToInt32(got) != tc.want {
				t.Errorf("maxTokens = %v, want %d", got, tc.want)
			}
		})
	}
}

func TestConvertFormats_AddedTypes(t *testing.T) {
	for mime, want := range map[string]types.DocumentFormat{
		"application/msword":       types.DocumentFormatDoc,
		"application/vnd.ms-excel": types.DocumentFormatXls,
		"text/html":                types.DocumentFormatHtml,
	} {
		if got, ok := convertDocumentFormat(mime); !ok || got != want {
			t.Errorf("%s = %q, want %q", mime, got, want)
		}
	}

	for mime, want := range map[string]types.VideoFormat{
		"video/x-flv":    types.VideoFormatFlv,
		"video/mpeg":     types.VideoFormatMpeg,
		"video/x-ms-wmv": types.VideoFormatWmv,
		"video/3gpp":     types.VideoFormatThreeGp,
	} {
		if got, ok := convertVideoFormat(mime); !ok || got != want {
			t.Errorf("%s = %q, want %q", mime, got, want)
		}
	}
}

func TestConvertToolResultFile_Video(t *testing.T) {
	block, err := convertToolResultFile(&provider.File{ContentType: "video/mp4", Content: []byte{1, 2, 3}})
	if err != nil {
		t.Fatal(err)
	}

	video, ok := block.(*types.ToolResultContentBlockMemberVideo)
	if !ok || video.Value.Format != types.VideoFormatMp4 {
		t.Fatalf("block = %T %+v", block, block)
	}
	if source, ok := video.Value.Source.(*types.VideoSourceMemberBytes); !ok || len(source.Value) != 3 {
		t.Fatalf("source = %T", video.Value.Source)
	}
}

// Bedrock Converse requires toolResult blocks to precede text blocks in the
// user message following an assistant toolUse turn, even when a user text item
// was supplied before or between function_call_output items.
func TestConvertConverseInput_ToolResultsPrecedeUserText(t *testing.T) {
	c := &Completer{Config: &Config{model: "eu.anthropic.claude-sonnet-4-6"}}

	history := []provider.Message{
		provider.UserMessage("Check weather"),
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "get_weather", Arguments: `{"city":"Zurich"}`}),
				provider.ToolCallContent(provider.ToolCall{ID: "call_2", Name: "get_weather", Arguments: `{"city":"Bern"}`}),
			},
		},
		provider.UserMessage("Here are the results:"),
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.ToolResultContent(provider.ToolResult{ID: "call_1", Parts: []provider.Part{{Text: "18C"}}}),
			},
		},
		// Unsigned foreign reasoning turn in the middle should drop cleanly and
		// still merge the surrounding user messages with toolResult blocks first.
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{Summary: provider.TextParts("unsigned foreign summary")}),
			},
		},
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.ToolResultContent(provider.ToolResult{ID: "call_2", Parts: []provider.Part{{Text: "16C"}}}),
			},
		},
	}

	req, err := c.convertConverseInput(history, &provider.CompleteOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if len(req.Messages) != 3 {
		t.Fatalf("messages = %d, want 3 (user, assistant, merged user)", len(req.Messages))
	}

	userBlocks := req.Messages[2].Content
	// Expect: toolResult(call_1), toolResult(call_2), text("Here are the results:"), cachePoint
	if len(userBlocks) != 4 {
		t.Fatalf("user blocks = %d, want 4", len(userBlocks))
	}
	if _, ok := userBlocks[0].(*types.ContentBlockMemberToolResult); !ok {
		t.Fatalf("block[0] = %T, want ToolResult", userBlocks[0])
	}
	if _, ok := userBlocks[1].(*types.ContentBlockMemberToolResult); !ok {
		t.Fatalf("block[1] = %T, want ToolResult", userBlocks[1])
	}
	if _, ok := userBlocks[2].(*types.ContentBlockMemberText); !ok {
		t.Fatalf("block[2] = %T, want Text", userBlocks[2])
	}

	// Omitted tools on a turn with tool history must still populate ToolConfig.
	if req.ToolConfig == nil || len(req.ToolConfig.Tools) == 0 {
		t.Fatal("expected ToolConfig to be populated from historical toolUse blocks when tools are omitted")
	}
	spec, ok := req.ToolConfig.Tools[0].(*types.ToolMemberToolSpec)
	if !ok || aws.ToString(spec.Value.Name) != "get_weather" {
		t.Fatalf("tool[0] = %T %+v, want get_weather spec", req.ToolConfig.Tools[0], req.ToolConfig.Tools[0])
	}
}

func TestConvertConverseInput_DisableParallelTools(t *testing.T) {
	for _, choice := range []provider.ToolChoice{"", provider.ToolChoiceAuto, provider.ToolChoiceAny, provider.ToolChoiceNone} {
		t.Run(string(choice), func(t *testing.T) {
			c := &Completer{Config: &Config{model: "eu.anthropic.claude-sonnet-4-6"}}
			req, err := c.convertConverseInput([]provider.Message{provider.UserMessage("check")}, &provider.CompleteOptions{
				Tools:       []provider.Tool{{Kind: provider.ToolKindFunction, Name: "lookup"}},
				ToolOptions: &provider.ToolOptions{Choice: choice, Allowed: []string{"lookup"}, DisableParallelToolCalls: true},
			})
			if err != nil {
				t.Fatal(err)
			}
			if choice == provider.ToolChoiceNone {
				if req.ToolConfig != nil {
					t.Fatal("none enabled tools")
				}
				return
			}
			data, err := req.AdditionalModelRequestFields.MarshalSmithyDocument()
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			tool, ok := fields["tool_choice"].(map[string]any)
			if !ok || len(tool) != 1 || tool["disable_parallel_tool_use"] != true {
				t.Fatalf("incorrect native control: %s", data)
			}
			if choice == provider.ToolChoiceAny {
				if _, ok := req.ToolConfig.ToolChoice.(*types.ToolChoiceMemberTool); !ok {
					t.Fatal("forced tool choice lost")
				}
			} else if req.ToolConfig.ToolChoice == nil {
				t.Fatal("default tool choice must supply auto alongside the Claude parallel control")
			}
		})
	}
	c := &Completer{Config: &Config{model: "amazon.nova-pro-v1:0"}}
	_, err := c.convertConverseInput([]provider.Message{provider.UserMessage("check")}, &provider.CompleteOptions{
		Tools: []provider.Tool{{Kind: provider.ToolKindFunction, Name: "lookup"}}, ToolOptions: &provider.ToolOptions{DisableParallelToolCalls: true},
	})
	if err != nil {
		t.Fatalf("unsupported controls must not reject non-Claude models: %v", err)
	}
}
