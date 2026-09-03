package features_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/responses"
)

// TestGPT6AstraE2E is intentionally excluded from DefaultModels: Astra is a
// higher-cost model and this probe should run only when explicitly requested.
// It makes one request to OpenAI and one through Wingman with the same model
// and payload, then compares the complete Responses API shape.
//
//	TEST_OPENAI_ASTRA_MODEL=gpt-6-astra \
//	  go test ./test/openai/responses/features -run '^TestGPT6AstraE2E$' -count=1 -v
func TestGPT6AstraE2E(t *testing.T) {
	model := strings.TrimSpace(os.Getenv("TEST_OPENAI_ASTRA_MODEL"))
	if model == "" {
		t.Skip("TEST_OPENAI_ASTRA_MODEL not set — skipping paid Astra probe")
	}
	if !strings.HasPrefix(strings.ToLower(model), "gpt-6-astra") {
		t.Fatalf("TEST_OPENAI_ASTRA_MODEL must name a gpt-6-astra model, got %q", model)
	}
	h := openai.New(t)
	h.SkipUnlessConfigured(t, model)

	request := responses.WithModel(map[string]any{
		"input": []any{
			map[string]any{
				"role":    "assistant",
				"phase":   "commentary",
				"content": "I am preparing the compatibility probe.",
			},
			map[string]any{
				"role":    "assistant",
				"phase":   "final_answer",
				"content": "The previous compatibility probe completed.",
			},
			map[string]any{
				"type":      "configuration_update",
				"reasoning": map[string]any{"effort": "low"},
			},
			map[string]any{
				"role":    "user",
				"content": `Call record_probe exactly once with value "ok". Do not write any text.`,
			},
		},
		"max_output_tokens":   256,
		"parallel_tool_calls": false,
		"reasoning":           map[string]any{"effort": "low", "context": "all_turns"},
		"store":               false,
		"text":                map[string]any{"verbosity": "low"},
		"truncation":          "disabled",
		"tools": []any{map[string]any{
			"type":        "function",
			"name":        "record_probe",
			"description": "Record the Astra end-to-end probe result.",
			"strict":      true,
			"async":       true,
			"parameters": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"value": map[string]any{"type": "string"},
				},
				"required": []string{"value"},
			},
		}},
		"tool_choice": map[string]any{
			"type": "function",
			"name": "record_probe",
		},
	}, model)

	ctx := context.Background()
	// Probe Wingman first so a stopped or misconfigured server cannot spend the
	// direct OpenAI comparison call.
	wingmanResp, err := h.Client.Post(ctx, h.Wingman, "/responses", request)
	if err != nil {
		t.Fatalf("Wingman Astra request failed: %v", err)
	}
	if wingmanResp.StatusCode != 200 {
		t.Fatalf("Wingman returned status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
	}

	openaiResp, err := h.Client.Post(ctx, h.OpenAI, "/responses", request)
	if err != nil {
		t.Fatalf("OpenAI Astra request failed: %v", err)
	}
	if openaiResp.StatusCode != 200 {
		t.Fatalf("OpenAI returned status %d: %s", openaiResp.StatusCode, string(openaiResp.RawBody))
	}

	requireAstraAsyncCall(t, "OpenAI", openaiResp.Body)
	requireAstraAsyncCall(t, "Wingman", wingmanResp.Body)

	rules := openai.DefaultResponsesResponseRules()
	rules["model"] = harness.FieldExact
	rules["store"] = harness.FieldExact
	harness.CompareStructure(t, "Astra response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
}

func requireAstraAsyncCall(t *testing.T, label string, body map[string]any) {
	t.Helper()

	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "low" {
		t.Fatalf("[%s] expected reasoning effort low, got %#v", label, body["reasoning"])
	}

	output, ok := body["output"].([]any)
	if !ok {
		t.Fatalf("[%s] output is not an array: %#v", label, body["output"])
	}
	for _, item := range output {
		call, ok := item.(map[string]any)
		if !ok || call["type"] != "function_call" || call["name"] != "record_probe" {
			continue
		}
		if call["async"] != true {
			t.Fatalf("[%s] expected function call async=true, got %#v", label, call["async"])
		}
		return
	}

	t.Fatalf("[%s] no record_probe function call: %#v", label, output)
}
