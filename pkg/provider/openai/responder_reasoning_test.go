package openai

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3/responses"
)

func TestRequiredReasoningRequestCompatibility(t *testing.T) {
	type testCase struct {
		name       string
		reasoning  *provider.ReasoningOptions
		wantEffort string
	}
	tests := []testCase{
		{name: "default"},
		{name: "adaptive", reasoning: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive}, wantEffort: "medium"},
		{name: "none", reasoning: &provider.ReasoningOptions{Type: provider.ReasoningTypeDisabled}, wantEffort: "low"},
		{name: "minimal", reasoning: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, Effort: provider.EffortMinimal}, wantEffort: "low"},
	}
	for _, effort := range []provider.Effort{provider.EffortLow, provider.EffortMedium, provider.EffortHigh, provider.EffortXHigh, provider.EffortMax} {
		tests = append(tests, testCase{
			name:       string(effort),
			reasoning:  &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, Effort: effort},
			wantEffort: string(effort),
		})
	}

	for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol", "GPT-6.1-SOL"} {
		for _, tt := range tests {
			t.Run(model+"/"+tt.name, func(t *testing.T) {
				options := &provider.CompleteOptions{ReasoningOptions: tt.reasoning}

				responder, err := NewResponder("", model)
				if err != nil {
					t.Fatalf("new responder: %v", err)
				}
				responsesRequest, err := responder.convertResponsesRequest([]provider.Message{provider.UserMessage("hi")}, options)
				if err != nil {
					t.Fatalf("convert Responses request: %v", err)
				}
				assertRequiredReasoningRequest(t, responsesRequest, tt.wantEffort)

				completer, err := NewCompleter("", model)
				if err != nil {
					t.Fatalf("new completer: %v", err)
				}
				chatRequest, err := completer.convertCompletionRequest([]provider.Message{provider.UserMessage("hi")}, options)
				if err != nil {
					t.Fatalf("convert Chat request: %v", err)
				}
				assertRequiredReasoningRequest(t, chatRequest, tt.wantEffort)
			})
		}
	}
}

func TestGPT6OptionalReasoningRequestCompatibility(t *testing.T) {
	for _, model := range []string{"gpt-6.1-terra", "gpt-6-sol", "gpt-6-luna"} {
		t.Run(model, func(t *testing.T) {
			messages := []provider.Message{provider.UserMessage("hi")}
			responder, err := NewResponder("", model)
			if err != nil {
				t.Fatal(err)
			}

			high := &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, Effort: provider.EffortHigh}
			responsesRequest, err := responder.convertResponsesRequest(messages, &provider.CompleteOptions{
				ReasoningOptions: high,
			})
			if err != nil {
				t.Fatal(err)
			}
			if responsesRequest.Reasoning.Effort != "high" || responsesRequest.Temperature.Valid() {
				t.Fatalf("Responses reasoning request: effort=%q temperature=%v", responsesRequest.Reasoning.Effort, responsesRequest.Temperature)
			}

			none := &provider.ReasoningOptions{Type: provider.ReasoningTypeDisabled}
			responsesRequest, err = responder.convertResponsesRequest(messages, &provider.CompleteOptions{
				ReasoningOptions: none,
			})
			if err != nil {
				t.Fatal(err)
			}
			if responsesRequest.Reasoning.Effort != "none" || responsesRequest.Temperature.Valid() {
				t.Fatalf("Responses none request: effort=%q temperature=%v", responsesRequest.Reasoning.Effort, responsesRequest.Temperature)
			}
		})
	}
}

func assertRequiredReasoningRequest(t *testing.T, request any, wantEffort string) {
	t.Helper()

	data, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	var effort string
	if reasoning, ok := payload["reasoning"].(map[string]any); ok {
		effort, _ = reasoning["effort"].(string)
	} else {
		effort, _ = payload["reasoning_effort"].(string)
	}
	if effort != wantEffort {
		t.Fatalf("reasoning effort = %q, want %q; request=%s", effort, wantEffort, data)
	}
	for _, field := range []string{"temperature", "top_p", "top_k"} {
		if _, ok := payload[field]; ok {
			t.Fatalf("reasoning request unexpectedly includes %s: %s", field, data)
		}
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

func TestResponseToolCallAsyncUnmarshal(t *testing.T) {
	var fnCall responses.ResponseFunctionToolCall
	if err := json.Unmarshal([]byte(`{"type":"function_call","call_id":"c1","name":"fn","arguments":"{}","async":true}`), &fnCall); err != nil {
		t.Fatal(err)
	}
	if !fnCall.Async {
		t.Fatal("expected async=true on function_call")
	}

	var customCall responses.ResponseCustomToolCall
	if err := json.Unmarshal([]byte(`{"type":"custom_tool_call","call_id":"c2","name":"custom","input":"x","async":true}`), &customCall); err != nil {
		t.Fatal(err)
	}
	if !customCall.Async {
		t.Fatal("expected async=true on custom_tool_call")
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
					Summary: provider.TextParts("visible summary"),
					Content: provider.TextParts("visible reasoning"),
				}),
				provider.ReasoningContent(provider.Reasoning{
					ID:        "rs_signed",
					Summary:   provider.TextParts("signed summary"),
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

func TestConvertResponsesRequest_OmitsReasoningTextForOpenAI(t *testing.T) {
	messages := []provider.Message{
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{
					ID:        "rs_signed",
					Summary:   provider.TextParts("signed summary"),
					Content:   provider.TextParts("signed reasoning", "second part"),
					Signature: "ENC_123",
				}),
			},
		},
	}

	tests := []struct {
		endpoint string
		content  bool
	}{
		{endpoint: "", content: false},
		{endpoint: "https://api.openai.com/v1", content: false},
		{endpoint: "https://test.openai.azure.com/openai/v1", content: false},
		{endpoint: "http://localhost:8000/v1", content: true},
	}

	for _, tt := range tests {
		responder, err := NewResponder(tt.endpoint, "gpt-5.4")
		if err != nil {
			t.Fatalf("new responder for %q: %v", tt.endpoint, err)
		}
		request, err := responder.convertResponsesRequest(messages, &provider.CompleteOptions{})
		if err != nil {
			t.Fatalf("convert request for %q: %v", tt.endpoint, err)
		}
		reasoning := reasoningInputItems(t, request)
		if len(reasoning) != 1 || reasoning[0]["encrypted_content"] != "ENC_123" {
			t.Fatalf("request for %q lost signed reasoning: %+v", tt.endpoint, reasoning)
		}
		summary, _ := reasoning[0]["summary"].([]any)
		if len(summary) != 1 {
			t.Fatalf("request for %q lost reasoning summary: %+v", tt.endpoint, reasoning[0])
		}
		content, _ := reasoning[0]["content"].([]any)
		if (len(content) > 0) != tt.content || tt.content && len(content) != 2 {
			t.Fatalf("request for %q reasoning content = %+v, want present=%v", tt.endpoint, reasoning[0]["content"], tt.content)
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
