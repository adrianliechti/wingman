package bedrock

import (
	"encoding/base64"
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

// TestConverseAdditionalFields_SchemaDisablesThinking verifies schema mode on
// models that think by default (Sonnet 5, Opus 5) sends an explicit disable —
// Bedrock rejects forced tool_choice while thinking is active, and omitting
// the field leaves it active on these models.
func TestConverseAdditionalFields_SchemaDisablesThinking(t *testing.T) {
	c := &Completer{Config: &Config{model: "eu.anthropic.claude-sonnet-5"}}

	fields, thinking := c.converseAdditionalFields(nil, &provider.CompleteOptions{
		Schema: &provider.Schema{Name: "classify", Properties: testSchema},
	})

	if thinking {
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

	fields, _ := c.converseAdditionalFields(nil, &provider.CompleteOptions{
		Schema: &provider.Schema{Name: "classify", Properties: testSchema},
	})

	if _, present := fields["thinking"]; present {
		t.Errorf("expected thinking omitted, got %v", fields["thinking"])
	}
}

// TestConverseAdditionalFields_DisabledThinkingCapsEffort verifies Claude
// Opus 5 — which rejects an explicit disable at effort xhigh/max — gets the
// effort capped to high when schema mode forces thinking off.
func TestConverseAdditionalFields_DisabledThinkingCapsEffort(t *testing.T) {
	c := &Completer{Config: &Config{model: "eu.anthropic.claude-opus-5"}}

	fields, _ := c.converseAdditionalFields(nil, &provider.CompleteOptions{
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

// TestConverseAdditionalFields_UnsignedToolHistoryDisablesThinking verifies
// adaptive thinking is turned off when the last assistant message carries
// tool calls without a signed thinking block (e.g. signatures stripped for
// portability): Claude over Bedrock rejects such requests with "Expected
// thinking or redacted_thinking, but found tool_use".
func TestConverseAdditionalFields_UnsignedToolHistoryDisablesThinking(t *testing.T) {
	c := &Completer{Config: &Config{model: "eu.anthropic.claude-sonnet-5"}}

	options := &provider.CompleteOptions{
		ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive},
	}

	stripped := []provider.Message{
		provider.UserMessage("list files"),
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{Text: "unsigned thought"}),
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

	fields, thinking := c.converseAdditionalFields(stripped, options)

	if thinking {
		t.Error("expected thinking not enabled")
	}
	got, _ := fields["thinking"].(map[string]any)
	if got["type"] != "disabled" {
		t.Fatalf("thinking: got %v, want disabled", fields["thinking"])
	}

	signed := append([]provider.Message{}, stripped...)
	signed[1] = provider.Message{
		Role: provider.MessageRoleAssistant,
		Content: []provider.Content{
			provider.ReasoningContent(provider.Reasoning{Text: "thought", Signature: "SIG"}),
			provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "ls", Arguments: "{}"}),
		},
	}

	fields, thinking = c.converseAdditionalFields(signed, options)

	if !thinking {
		t.Error("expected thinking enabled for signed history")
	}
	got, _ = fields["thinking"].(map[string]any)
	if got["type"] != "adaptive" {
		t.Fatalf("signed history thinking: got %v, want adaptive", fields["thinking"])
	}
}

// TestConvertAssistantContent_GroupsBlocks verifies assistant blocks are
// grouped reasoning -> text -> toolUse regardless of input order: Bedrock
// rejects turns where a text block separates toolUse from its toolResult.
func TestConvertAssistantContent_GroupsBlocks(t *testing.T) {
	content, err := convertAssistantContent(provider.Message{
		Role: provider.MessageRoleAssistant,
		Content: []provider.Content{
			provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "step_one", Arguments: "{}"}),
			provider.TextContent("halfway update"),
			provider.ReasoningContent(provider.Reasoning{Text: "thought", Signature: "SIG"}),
			provider.ToolCallContent(provider.ToolCall{ID: "call_2", Name: "step_two", Arguments: "{}"}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var kinds []string
	for _, block := range content {
		switch block.(type) {
		case *types.ContentBlockMemberReasoningContent:
			kinds = append(kinds, "reasoning")
		case *types.ContentBlockMemberText:
			kinds = append(kinds, "text")
		case *types.ContentBlockMemberToolUse:
			kinds = append(kinds, "tool_use")
		}
	}

	want := []string{"reasoning", "text", "tool_use", "tool_use"}
	if !slices.Equal(kinds, want) {
		t.Fatalf("block order: got %v, want %v", kinds, want)
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
			provider.ReasoningContent(provider.Reasoning{Text: "step", Signature: "SIG"}),
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
