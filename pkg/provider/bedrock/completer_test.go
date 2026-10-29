package bedrock

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

var testSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"title": map[string]any{"type": "string"},
	},
	"required":             []string{"title"},
	"additionalProperties": false,
}

func TestConvertConverseInputUsesForcedToolForSchema(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-opus-4-8-v1:0"}}

	req, err := c.convertConverseInput([]provider.Message{
		provider.UserMessage("Return JSON."),
	}, &provider.CompleteOptions{
		Schema: &provider.Schema{
			Name:       "classify_chat",
			Properties: testSchema,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if req.ToolConfig == nil {
		t.Fatal("expected tool config for schema mode")
	}

	choice, ok := req.ToolConfig.ToolChoice.(*types.ToolChoiceMemberTool)
	if !ok {
		t.Fatalf("expected forced tool choice, got %T", req.ToolConfig.ToolChoice)
	}
	if got := *choice.Value.Name; got != "classify_chat" {
		t.Fatalf("expected forced tool %q, got %q", "classify_chat", got)
	}

	var found bool
	for _, tool := range req.ToolConfig.Tools {
		spec, ok := tool.(*types.ToolMemberToolSpec)
		if ok && *spec.Value.Name == "classify_chat" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected schema tool in tool config")
	}
}

// TestConvertConverseInputSteersSchemaToolWithoutForcing verifies models that
// reject forced tool choice (Opus 5.5) get the schema tool with automatic
// selection, and keep thinking on instead of sending the rejected disable.
func TestConvertConverseInputSteersSchemaToolWithoutForcing(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-opus-5-5"}}

	options := &provider.CompleteOptions{
		Schema: &provider.Schema{
			Name:       "classify_chat",
			Properties: testSchema,
		},
		ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, Effort: provider.EffortXHigh},
	}

	req, err := c.convertConverseInput([]provider.Message{
		provider.UserMessage("Return JSON."),
	}, options)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := req.ToolConfig.ToolChoice.(*types.ToolChoiceMemberAuto); !ok {
		t.Fatalf("expected auto tool choice, got %T", req.ToolConfig.ToolChoice)
	}

	fields, thinking := c.converseAdditionalFields(options)
	if !thinking.Enabled || thinking.Disabled {
		t.Fatalf("expected thinking to stay enabled, got %+v", thinking)
	}
	if got, _ := fields["thinking"].(map[string]any); got["type"] != "adaptive" {
		t.Errorf("thinking: got %v, want adaptive", fields["thinking"])
	}
}

// TestConverseAdditionalFieldsSendsBetweenTools verifies Claude Sonnet 5.5,
// which rejects an explicit disable, gets its lowest setting instead, with
// the effort capped at high.
func TestConverseAdditionalFieldsSendsBetweenTools(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-sonnet-5-5"}}

	fields, _ := c.converseAdditionalFields(&provider.CompleteOptions{
		ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeDisabled, Effort: provider.EffortXHigh},
	})

	if got, _ := fields["thinking"].(map[string]any); got["type"] != "between_tools" || len(got) != 1 {
		t.Fatalf("thinking: got %v, want between_tools", fields["thinking"])
	}
	if got, _ := fields["output_config"].(map[string]any); got["effort"] != "high" {
		t.Errorf("effort: got %v, want high", fields["output_config"])
	}
}

// TestConverseAdditionalFieldsProgressUpdates verifies display "updates" is
// sent with its beta, which Bedrock requires for the value.
func TestConverseAdditionalFieldsProgressUpdates(t *testing.T) {
	c := &Completer{Config: &Config{model: "eu.anthropic.claude-opus-5-5"}}

	fields, _ := c.converseAdditionalFields(&provider.CompleteOptions{
		ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, IncludeUpdates: true},
	})

	if got, _ := fields["thinking"].(map[string]any); got["display"] != "updates" {
		t.Fatalf("thinking: got %v, want display updates", fields["thinking"])
	}
	if got, _ := fields["anthropic_beta"].([]string); !slices.Contains(got, "thinking-display-updates-2026-08-18") {
		t.Errorf("anthropic_beta: got %v", fields["anthropic_beta"])
	}
}

// TestConvertConverseInputRejectsForcedToolChoice verifies forced tool choice
// on a model that rejects it returns an explicit error instead of an
// upstream 400.
func TestConvertConverseInputRejectsForcedToolChoice(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-opus-5-5"}}

	_, err := c.convertConverseInput([]provider.Message{
		provider.UserMessage("hi"),
	}, &provider.CompleteOptions{
		Tools:       []provider.Tool{{Name: "get_weather", Parameters: map[string]any{"type": "object"}}},
		ToolOptions: &provider.ToolOptions{Choice: provider.ToolChoiceAny},
	})
	if err == nil {
		t.Fatal("expected forced tool choice to be rejected")
	}
}

// TestConverseAdditionalFields_SchemaDisablesThinking verifies schema mode on
// models that think by default (Sonnet 5, Opus 5) sends an explicit disable —
// Bedrock rejects forced tool_choice while thinking is active, and omitting
// the field leaves it active on these models.
func TestConverseAdditionalFields_SchemaDisablesThinking(t *testing.T) {
	c := &Completer{Config: &Config{model: "eu.anthropic.claude-sonnet-5"}}

	fields, thinking := c.converseAdditionalFields(&provider.CompleteOptions{
		Schema: &provider.Schema{Name: "classify", Properties: testSchema},
	})

	if thinking.Enabled {
		t.Error("expected thinking not enabled")
	}

	got, _ := fields["thinking"].(map[string]any)
	if got["type"] != "disabled" {
		t.Errorf("thinking: got %v, want disabled", fields["thinking"])
	}
}

// TestConverseAdditionalFields_SchemaOmitsThinkingForOlderModels verifies
// models where omitting the field already means no thinking (Opus 4.8) keep
// the omit behavior instead of sending an unnecessary disable.
func TestConverseAdditionalFields_SchemaOmitsThinkingForOlderModels(t *testing.T) {
	c := &Completer{Config: &Config{model: "eu.anthropic.claude-opus-4-8"}}

	fields, _ := c.converseAdditionalFields(&provider.CompleteOptions{
		Schema: &provider.Schema{Name: "classify", Properties: testSchema},
	})

	if _, present := fields["thinking"]; present {
		t.Errorf("expected thinking omitted, got %v", fields["thinking"])
	}
}

// TestConverseAdditionalFields_ThinkingDisplay verifies the display is always
// explicit: Claude 5.x models omit thinking text by default, so a requested
// summary must ask for it.
func TestConverseAdditionalFields_ThinkingDisplay(t *testing.T) {
	c := &Completer{Config: &Config{model: "eu.anthropic.claude-opus-5-5"}}

	for _, summarized := range []bool{true, false} {
		fields, _ := c.converseAdditionalFields(&provider.CompleteOptions{
			ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, IncludeSummary: summarized},
		})

		want := "omitted"
		if summarized {
			want = "summarized"
		}

		got, _ := fields["thinking"].(map[string]any)
		if got["type"] != "adaptive" || got["display"] != want {
			t.Errorf("summary %t: thinking = %v, want adaptive with display %s", summarized, fields["thinking"], want)
		}
	}
}

// TestConverseAdditionalFields_DisabledThinkingCapsEffort verifies Claude
// Opus 5 — which rejects an explicit disable at effort xhigh/max — gets the
// effort capped to high when schema mode forces thinking off.
func TestConverseAdditionalFields_DisabledThinkingCapsEffort(t *testing.T) {
	c := &Completer{Config: &Config{model: "eu.anthropic.claude-opus-5"}}

	fields, _ := c.converseAdditionalFields(&provider.CompleteOptions{
		Schema:           &provider.Schema{Name: "classify", Properties: testSchema},
		ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, Effort: provider.EffortXHigh},
	})

	got, _ := fields["thinking"].(map[string]any)
	if got["type"] != "disabled" {
		t.Fatalf("thinking: got %v, want disabled", fields["thinking"])
	}

	config, _ := fields["output_config"].(map[string]any)
	if config["effort"] != "high" {
		t.Errorf("effort: got %v, want high", config["effort"])
	}
}

// TestConvertConverseInput_UnsignedToolHistoryKeepsThinking verifies
// adaptive thinking stays on when the last assistant message carries tool
// calls without a signed thinking block (e.g. signatures stripped for
// portability): the unsigned reasoning is not replayed, adaptive thinking
// accepts a tool turn without a thinking block, and a stable thinking
// parameter keeps the prompt cache prefix intact across turns.
func TestConvertConverseInput_UnsignedToolHistoryKeepsThinking(t *testing.T) {
	c := &Completer{Config: &Config{model: "eu.anthropic.claude-sonnet-5"}}

	options := &provider.CompleteOptions{
		ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive},
	}

	stripped := []provider.Message{
		provider.UserMessage("list files"),
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{Content: provider.TextParts("unsigned thought")}),
				provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "ls", Arguments: "{}"}),
			},
		},
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.ToolResultContent(provider.ToolResult{ID: "call_1", Parts: []provider.Part{{Text: "main.go"}}}),
			},
		},
	}

	checkThinking := func(messages []provider.Message) {
		t.Helper()
		req, err := c.convertConverseInput(messages, options)
		if err != nil {
			t.Fatal(err)
		}
		data, err := req.AdditionalModelRequestFields.MarshalSmithyDocument()
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		got, _ := fields["thinking"].(map[string]any)
		if got["type"] != "adaptive" {
			t.Fatalf("thinking: got %v, want adaptive", fields["thinking"])
		}
	}
	checkThinking(stripped)

	signed := append([]provider.Message{}, stripped...)
	signed[1] = provider.Message{
		Role: provider.MessageRoleAssistant,
		Content: []provider.Content{
			provider.ReasoningContent(provider.Reasoning{Content: provider.TextParts("thought"), Signature: "SIG"}),
			provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "ls", Arguments: "{}"}),
		},
	}

	checkThinking(signed)
}

// TestConvertAssistantContent_KeepsOrder verifies blocks replay in the order
// the model produced them, which Bedrock accepts in any arrangement.
func TestConvertAssistantContent_KeepsOrder(t *testing.T) {
	cases := []struct {
		name    string
		content []provider.Content
		want    []string
	}{
		{"text after calls", []provider.Content{
			provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "step_one", Arguments: "{}"}),
			provider.TextContent("halfway update"),
			provider.ReasoningContent(provider.Reasoning{Content: provider.TextParts("thought"), Signature: "SIG"}),
			provider.ToolCallContent(provider.ToolCall{ID: "call_2", Name: "step_two", Arguments: "{}"}),
		}, []string{"tool_use", "text", "reasoning:thought", "tool_use"}},
		{"interleaved thinking", []provider.Content{
			provider.ReasoningContent(provider.Reasoning{Content: provider.TextParts("plan"), Signature: "SIG1"}),
			provider.TextContent("first"),
			provider.ReasoningContent(provider.Reasoning{Content: provider.TextParts("check"), Signature: "SIG2"}),
			provider.TextContent("second"),
			provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "step_one", Arguments: "{}"}),
		}, []string{"reasoning:plan", "text", "reasoning:check", "text", "tool_use"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content, err := convertAssistantContent(provider.Message{Role: provider.MessageRoleAssistant, Content: tc.content})
			if err != nil {
				t.Fatal(err)
			}

			var kinds []string
			for _, block := range content {
				switch block := block.(type) {
				case *types.ContentBlockMemberReasoningContent:
					text := block.Value.(*types.ReasoningContentBlockMemberReasoningText).Value.Text
					kinds = append(kinds, "reasoning:"+aws.ToString(text))
				case *types.ContentBlockMemberText:
					kinds = append(kinds, "text")
				case *types.ContentBlockMemberToolUse:
					kinds = append(kinds, "tool_use")
				}
			}

			if !slices.Equal(kinds, tc.want) {
				t.Fatalf("block order: got %v, want %v", kinds, tc.want)
			}
		})
	}
}

// TestConvertAssistantContent_Reasoning verifies signed thinking maps to a
// reasoning text block and redacted thinking to a redacted content block with
// the blob decoded from base64.
func TestConvertAssistantContent_Reasoning(t *testing.T) {
	blob := []byte{0x01, 0x02, 0x03, 0xff}

	content, err := convertAssistantContent(provider.Message{
		Role: provider.MessageRoleAssistant,
		Content: []provider.Content{
			provider.ReasoningContent(provider.Reasoning{Content: provider.TextParts("step"), Signature: "SIG"}),
			provider.ReasoningContent(provider.Reasoning{Signature: base64.StdEncoding.EncodeToString(blob), Redacted: true}),
			provider.TextContent("answer"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(content) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(content))
	}

	first, ok := content[0].(*types.ContentBlockMemberReasoningContent)
	if !ok {
		t.Fatalf("block 0: got %T", content[0])
	}
	text, ok := first.Value.(*types.ReasoningContentBlockMemberReasoningText)
	if !ok {
		t.Fatalf("block 0: got %T", first.Value)
	}
	if *text.Value.Text != "step" || *text.Value.Signature != "SIG" {
		t.Errorf("block 0: %+v", text.Value)
	}

	second, ok := content[1].(*types.ContentBlockMemberReasoningContent)
	if !ok {
		t.Fatalf("block 1: got %T", content[1])
	}
	redacted, ok := second.Value.(*types.ReasoningContentBlockMemberRedactedContent)
	if !ok {
		t.Fatalf("block 1: got %T", second.Value)
	}
	if string(redacted.Value) != string(blob) {
		t.Errorf("block 1: got %v, want %v", redacted.Value, blob)
	}

	if _, ok := content[2].(*types.ContentBlockMemberText); !ok {
		t.Fatalf("block 2: got %T", content[2])
	}
}

func TestConvertUserContent_ToolResultError(t *testing.T) {
	content, err := convertUserContent(provider.Message{
		Role: provider.MessageRoleUser,
		Content: []provider.Content{
			provider.ToolResultContent(provider.ToolResult{
				ID:      "toolu_1",
				IsError: true,
				Parts:   []provider.Part{{Text: "permission denied"}},
			}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(content) != 1 {
		t.Fatalf("expected 1 block, got %d", len(content))
	}

	result, ok := content[0].(*types.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("block 0: got %T", content[0])
	}
	if result.Value.Status != types.ToolResultStatusError {
		t.Fatalf("status: got %q, want %q", result.Value.Status, types.ToolResultStatusError)
	}
}

// TestToUsage_CacheInclusiveInputTokens verifies the intermediate Usage uses a
// cache-inclusive InputTokens total. Bedrock reports InputTokens as only the
// fresh (non-cached) tokens, with cache read/write counted separately, so the
// mapping must fold both back into InputTokens while still exposing the cached
// subset in the cache fields.
func TestToUsage_CacheInclusiveInputTokens(t *testing.T) {
	usage := toUsage(&types.TokenUsage{
		InputTokens:           aws.Int32(10),
		OutputTokens:          aws.Int32(7),
		CacheReadInputTokens:  aws.Int32(40),
		CacheWriteInputTokens: aws.Int32(50),
	})

	if usage == nil {
		t.Fatal("expected usage")
	}

	if usage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100 (10 fresh + 40 read + 50 write)", usage.InputTokens)
	}
	if usage.OutputTokens != 7 {
		t.Errorf("OutputTokens = %d, want 7", usage.OutputTokens)
	}
	if usage.CacheReadInputTokens != 40 {
		t.Errorf("CacheReadInputTokens = %d, want 40", usage.CacheReadInputTokens)
	}
	if usage.CacheCreationInputTokens != 50 {
		t.Errorf("CacheCreationInputTokens = %d, want 50", usage.CacheCreationInputTokens)
	}

	// Cache fields must always be a subset of the inclusive input total.
	if usage.CacheReadInputTokens+usage.CacheCreationInputTokens > usage.InputTokens {
		t.Errorf("cache tokens (%d+%d) exceed InputTokens (%d)",
			usage.CacheReadInputTokens, usage.CacheCreationInputTokens, usage.InputTokens)
	}
}

func TestToUsage_NilReturnsNil(t *testing.T) {
	if usage := toUsage(nil); usage != nil {
		t.Fatalf("expected nil usage, got %+v", usage)
	}
}

// For Claude models, wingman injects Bedrock Converse cachePoint blocks (type
// "default") after the system blocks, on the last user message, and after the
// tool definitions — the documented tools→system→messages cache layout, three
// of the four allowed checkpoints. Sub-threshold prefixes just don't cache (no
// error), so no token-minimum guard is needed.
func TestConvert_CachePointPlacementForClaude(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-sonnet-4-6"}}

	sys := c.convertSystem([]provider.Message{provider.SystemMessage("You are helpful.")})
	if len(sys) == 0 {
		t.Fatal("expected system blocks")
	}
	cp, ok := sys[len(sys)-1].(*types.SystemContentBlockMemberCachePoint)
	if !ok {
		t.Fatalf("expected trailing system cachePoint, got %T", sys[len(sys)-1])
	}
	if cp.Value.Type != types.CachePointTypeDefault {
		t.Errorf("system cachePoint type = %q, want default", cp.Value.Type)
	}

	msgs, err := c.convertMessages([]provider.Message{provider.UserMessage("Hello there.")})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected messages")
	}
	last := msgs[len(msgs)-1].Content
	if _, ok := last[len(last)-1].(*types.ContentBlockMemberCachePoint); !ok {
		t.Errorf("expected trailing cachePoint on last user message, got %T", last[len(last)-1])
	}

	tc, _ := c.convertToolConfig([]provider.Tool{{Name: "get_weather", Description: "x", Parameters: testSchema}}, nil)
	if tc == nil || len(tc.Tools) == 0 {
		t.Fatal("expected tool config")
	}
	if _, ok := tc.Tools[len(tc.Tools)-1].(*types.ToolMemberCachePoint); !ok {
		t.Errorf("expected trailing tool cachePoint, got %T", tc.Tools[len(tc.Tools)-1])
	}
}

// Non-Claude Bedrock models (Nova/Titan/Llama) must NOT get cachePoint blocks —
// their cache support differs and an unsupported cachePoint can be rejected.
func TestConvert_NoCachePointForNonClaude(t *testing.T) {
	c := &Completer{Config: &Config{model: "amazon.nova-pro-v1:0"}}

	for _, b := range c.convertSystem([]provider.Message{provider.SystemMessage("You are helpful.")}) {
		if _, ok := b.(*types.SystemContentBlockMemberCachePoint); ok {
			t.Error("non-Claude model must not get a system cachePoint")
		}
	}

	msgs, err := c.convertMessages([]provider.Message{provider.UserMessage("Hello there.")})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		for _, b := range m.Content {
			if _, ok := b.(*types.ContentBlockMemberCachePoint); ok {
				t.Error("non-Claude model must not get a message cachePoint")
			}
		}
	}

	if tc, _ := c.convertToolConfig([]provider.Tool{{Name: "get_weather", Description: "x", Parameters: testSchema}}, nil); tc != nil {
		for _, tl := range tc.Tools {
			if _, ok := tl.(*types.ToolMemberCachePoint); ok {
				t.Error("non-Claude model must not get a tool cachePoint")
			}
		}
	}
}
