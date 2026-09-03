package openai

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3/responses"
)

func TestGPT6AstraNormalizesUnsupportedReasoningEfforts(t *testing.T) {
	tests := []struct {
		name      string
		reasoning provider.ReasoningOptions
	}{
		{name: "none", reasoning: provider.ReasoningOptions{Type: provider.ReasoningTypeDisabled}},
		{name: "minimal", reasoning: provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, Effort: provider.EffortMinimal}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			temperature := float32(0.7)
			options := &provider.CompleteOptions{ReasoningOptions: &tt.reasoning, Temperature: &temperature}

			responder, err := NewResponder("", "gpt-6-astra")
			if err != nil {
				t.Fatalf("new responder: %v", err)
			}
			responsesRequest, err := responder.convertResponsesRequest([]provider.Message{provider.UserMessage("hi")}, options)
			if err != nil {
				t.Fatalf("convert Responses request: %v", err)
			}
			assertAstraRequestCompatibility(t, responsesRequest)

			completer, err := NewCompleter("", "gpt-6-astra")
			if err != nil {
				t.Fatalf("new completer: %v", err)
			}
			chatRequest, err := completer.convertCompletionRequest([]provider.Message{provider.UserMessage("hi")}, options)
			if err != nil {
				t.Fatalf("convert Chat request: %v", err)
			}
			assertAstraRequestCompatibility(t, chatRequest)
		})
	}
}

func assertAstraRequestCompatibility(t *testing.T, request any) {
	t.Helper()

	data, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	var effort any
	if reasoning, ok := payload["reasoning"].(map[string]any); ok {
		effort = reasoning["effort"]
	} else {
		effort = payload["reasoning_effort"]
	}
	if effort != "low" {
		t.Fatalf("reasoning effort = %v, want low; request=%s", effort, data)
	}
	if _, ok := payload["temperature"]; ok {
		t.Fatalf("Astra request unexpectedly includes temperature: %s", data)
	}
}

func TestGPT6AstraResponsesFeaturesRoundTrip(t *testing.T) {
	async := true
	responder, err := NewResponder("", "gpt-6-astra")
	if err != nil {
		t.Fatalf("new responder: %v", err)
	}

	messages := []provider.Message{
		provider.UserMessage("start"),
		{
			Role:    provider.MessageRoleAssistant,
			Phase:   provider.MessagePhaseCommentary,
			Content: []provider.Content{provider.TextContent("working")},
		},
		{
			Content: []provider.Content{provider.CompactionTriggerContent()},
		},
		{
			Content: []provider.Content{provider.ConfigurationUpdateContent(provider.ConfigurationUpdate{
				ReasoningEffort: provider.EffortHigh,
			})},
		},
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{ID: "call_fn", Async: true, Name: "lookup", Arguments: `{}`}),
				provider.ToolCallContent(provider.ToolCall{ID: "call_custom", Async: true, Kind: provider.ToolKindCustom, Name: "query", Arguments: "select 1"}),
			},
		},
	}

	req, err := responder.convertResponsesRequest(messages, &provider.CompleteOptions{Tools: []provider.Tool{
		{Name: "lookup", Async: &async, Parameters: map[string]any{"type": "object"}},
		{Name: "query", Kind: provider.ToolKindCustom, Async: &async},
	}})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var payload struct {
		Truncation string           `json:"truncation"`
		Input      []map[string]any `json:"input"`
		Tools      []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if payload.Truncation != "disabled" {
		t.Fatalf("truncation = %q, want disabled", payload.Truncation)
	}
	if len(payload.Input) != 6 {
		t.Fatalf("input item count = %d, want 6: %s", len(payload.Input), data)
	}
	if payload.Input[1]["phase"] != "commentary" {
		t.Fatalf("assistant phase lost: %v", payload.Input[1])
	}
	if payload.Input[2]["type"] != "compaction_trigger" {
		t.Fatalf("compaction trigger moved: %v", payload.Input[2])
	}
	update, ok := payload.Input[3]["reasoning"].(map[string]any)
	if payload.Input[3]["type"] != "configuration_update" || !ok || update["effort"] != "high" {
		t.Fatalf("configuration update lost: %v", payload.Input[3])
	}
	for _, i := range []int{4, 5} {
		if payload.Input[i]["async"] != true {
			t.Fatalf("async call lost at input[%d]: %v", i, payload.Input[i])
		}
	}
	for i, tool := range payload.Tools {
		if tool["async"] != true {
			t.Fatalf("async tool lost at tools[%d]: %v", i, tool)
		}
	}
}

func TestConfigurationUpdateRejectsAutomaticCompaction(t *testing.T) {
	responder, err := NewResponder("", "gpt-6-astra")
	if err != nil {
		t.Fatalf("new responder: %v", err)
	}

	_, err = responder.convertResponsesRequest([]provider.Message{{
		Content: []provider.Content{provider.ConfigurationUpdateContent(provider.ConfigurationUpdate{ReasoningEffort: provider.EffortHigh})},
	}}, &provider.CompleteOptions{CompactionOptions: &provider.CompactionOptions{Threshold: 10_000}})

	var providerErr *provider.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Code != 400 {
		t.Fatalf("error = %v, want ProviderError 400", err)
	}
}

func TestResponseToolCallAsyncReadsUnknownSDKField(t *testing.T) {
	if !responseToolCallAsync(`{"type":"function_call","async":true}`) {
		t.Fatal("expected async=true")
	}
	if responseToolCallAsync(`{"type":"function_call"}`) {
		t.Fatal("expected missing async to be false")
	}
}

// TestConvertResponsesRequest_ReasoningMax verifies effort "max" (GPT-5.6+,
// no SDK constant yet) passes through verbatim.
func TestConvertResponsesRequest_ReasoningMax(t *testing.T) {
	responder, err := NewResponder("", "gpt-5.6")
	if err != nil {
		t.Fatalf("new responder: %v", err)
	}

	req, err := responder.convertResponsesRequest([]provider.Message{provider.UserMessage("hi")}, &provider.CompleteOptions{
		ReasoningOptions: &provider.ReasoningOptions{
			Effort:  provider.EffortMax,
			Context: provider.ReasoningContextAllTurns,
		},
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m struct {
		Reasoning map[string]any `json:"reasoning"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if m.Reasoning["effort"] != "max" {
		t.Errorf("effort = %v, want max", m.Reasoning["effort"])
	}
	if m.Reasoning["context"] != "all_turns" {
		t.Errorf("context = %v, want all_turns", m.Reasoning["context"])
	}
}

// Replayed reasoning without encrypted_content is not portable across
// Responses API backends. Omit it in the OpenAI provider even when visible
// summary or reasoning text remains.
func TestConvertResponsesRequest_SkipsUnsignedReasoning(t *testing.T) {
	messages := []provider.Message{
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{
					ID:      "rs_unsigned",
					Summary: "visible summary",
					Text:    "visible reasoning",
				}),
				provider.ReasoningContent(provider.Reasoning{
					ID:        "rs_signed",
					Summary:   "signed summary",
					Signature: "ENC_123",
				}),
			},
		},
	}

	for _, endpoint := range []string{"", "https://test.openai.azure.com/openai/v1"} {
		responder, err := NewResponder(endpoint, "gpt-5.4")
		if err != nil {
			t.Fatalf("new responder for %q: %v", endpoint, err)
		}
		request, err := responder.convertResponsesRequest(messages, &provider.CompleteOptions{})
		if err != nil {
			t.Fatalf("convert request for %q: %v", endpoint, err)
		}
		reasoning := reasoningInputItems(t, request)
		if len(reasoning) != 1 || reasoning[0]["id"] != "rs_signed" || reasoning[0]["encrypted_content"] != "ENC_123" {
			t.Fatalf("request for %q retained unsigned reasoning: %+v", endpoint, reasoning)
		}
	}
}

func reasoningInputItems(t *testing.T, request *responses.ResponseNewParams) []map[string]any {
	t.Helper()

	data, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var payload struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	var result []map[string]any
	for _, item := range payload.Input {
		if item["type"] == "reasoning" {
			result = append(result, item)
		}
	}
	return result
}

// TestToResponseUsage_CacheWriteTokens verifies cache_write_tokens (GPT-5.6
// usage detail, not yet typed in the SDK) maps to CacheCreationInputTokens.
func TestToResponseUsage_CacheWriteTokens(t *testing.T) {
	var usage responses.ResponseUsage

	if err := json.Unmarshal([]byte(`{
		"input_tokens": 100,
		"output_tokens": 5,
		"total_tokens": 105,
		"input_tokens_details": {"cached_tokens": 40, "cache_write_tokens": 16},
		"output_tokens_details": {"reasoning_tokens": 2}
	}`), &usage); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	result := toResponseUsage(usage)
	if result == nil {
		t.Fatal("expected usage")
	}

	if result.CacheReadInputTokens != 40 {
		t.Errorf("CacheReadInputTokens = %d, want 40", result.CacheReadInputTokens)
	}
	if result.CacheCreationInputTokens != 16 {
		t.Errorf("CacheCreationInputTokens = %d, want 16", result.CacheCreationInputTokens)
	}
}
