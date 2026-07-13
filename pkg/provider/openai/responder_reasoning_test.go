package openai

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3/responses"
)

// TestConvertResponsesRequest_ReasoningMax verifies effort "max" (GPT-5.6+,
// no SDK constant yet) passes through verbatim.
func TestConvertResponsesRequest_ReasoningMax(t *testing.T) {
	responder, err := NewResponder("", "gpt-5.6")
	if err != nil {
		t.Fatalf("new responder: %v", err)
	}

	req, err := responder.convertResponsesRequest([]provider.Message{provider.UserMessage("hi")}, &provider.CompleteOptions{
		ReasoningOptions: &provider.ReasoningOptions{
			Effort: provider.EffortMax,
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
