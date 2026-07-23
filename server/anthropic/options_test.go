package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// TestToCompleteOptions_OutputConfigFormat verifies the canonical
// output_config.format parameter maps to a schema (the deprecated top-level
// output_format is the fallback).
func TestToCompleteOptions_OutputConfigFormat(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}

	options, err := toCompleteOptions(MessageRequest{
		OutputConfig: &OutputConfig{
			Format: &OutputFormat{Type: "json_schema", Schema: schema},
		},
	})
	if err != nil {
		t.Fatalf("toCompleteOptions: %v", err)
	}

	if options.Schema == nil {
		t.Fatal("expected schema from output_config.format")
	}
	if options.Schema.Name != "response" {
		t.Errorf("schema name: got %q, want default \"response\"", options.Schema.Name)
	}
	if options.Schema.Properties == nil {
		t.Error("expected schema properties")
	}
}

func TestToCompleteOptions_OutputConfigFormatPrecedence(t *testing.T) {
	options, err := toCompleteOptions(MessageRequest{
		OutputFormat: &OutputFormat{Type: "json_schema", Name: "legacy", Schema: map[string]any{"type": "object"}},
		OutputConfig: &OutputConfig{
			Format: &OutputFormat{Type: "json_schema", Name: "canonical", Schema: map[string]any{"type": "object"}},
		},
	})
	if err != nil {
		t.Fatalf("toCompleteOptions: %v", err)
	}

	if options.Schema == nil || options.Schema.Name != "canonical" {
		t.Fatalf("expected output_config.format to win, got %+v", options.Schema)
	}
}

func TestToCompleteOptions_LegacyOutputFormat(t *testing.T) {
	options, err := toCompleteOptions(MessageRequest{
		OutputFormat: &OutputFormat{Type: "json_schema", Name: "legacy", Schema: map[string]any{"type": "object"}},
	})
	if err != nil {
		t.Fatalf("toCompleteOptions: %v", err)
	}

	if options.Schema == nil || options.Schema.Name != "legacy" {
		t.Fatalf("expected legacy output_format schema, got %+v", options.Schema)
	}
}

func TestToCompleteOptions_Thinking(t *testing.T) {
	cases := []struct {
		name       string
		req        MessageRequest
		wantType   provider.ReasoningType
		wantEffort provider.Effort
		wantNil    bool
	}{
		{
			name:     "adaptive",
			req:      MessageRequest{Thinking: &ThinkingConfig{Type: "adaptive"}},
			wantType: provider.ReasoningTypeAdaptive,
		},
		{
			name:       "enabled with budget derives effort",
			req:        MessageRequest{Thinking: &ThinkingConfig{Type: "enabled", BudgetTokens: 2048}},
			wantType:   provider.ReasoningTypeAdaptive,
			wantEffort: provider.EffortLow,
		},
		{
			name:     "disabled",
			req:      MessageRequest{Thinking: &ThinkingConfig{Type: "disabled"}},
			wantType: provider.ReasoningTypeDisabled,
		},
		{
			name:       "effort only",
			req:        MessageRequest{OutputConfig: &OutputConfig{Effort: "xhigh"}},
			wantEffort: provider.EffortXHigh,
		},
		{
			name:    "no thinking",
			req:     MessageRequest{},
			wantNil: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options, err := toCompleteOptions(tc.req)
			if err != nil {
				t.Fatalf("toCompleteOptions: %v", err)
			}

			if tc.wantNil {
				if options.ReasoningOptions != nil {
					t.Fatalf("expected nil reasoning options, got %+v", options.ReasoningOptions)
				}
				return
			}

			if options.ReasoningOptions == nil {
				t.Fatal("expected reasoning options")
			}
			if options.ReasoningOptions.Type != tc.wantType {
				t.Errorf("type: got %q, want %q", options.ReasoningOptions.Type, tc.wantType)
			}
			if options.ReasoningOptions.Effort != tc.wantEffort {
				t.Errorf("effort: got %q, want %q", options.ReasoningOptions.Effort, tc.wantEffort)
			}
		})
	}
}

// TestToCompleteOptions_CompactionWithoutTrigger verifies a compaction edit
// without an explicit trigger still enables compaction (upstream default).
func TestToCompleteOptions_CompactionWithoutTrigger(t *testing.T) {
	options, err := toCompleteOptions(MessageRequest{
		ContextManagement: &ContextManagement{
			Edits: []ContextManagementEdit{{Type: "compact_20260112"}},
		},
	})
	if err != nil {
		t.Fatalf("toCompleteOptions: %v", err)
	}

	if options.CompactionOptions == nil {
		t.Fatal("expected compaction options")
	}
	if options.CompactionOptions.Threshold != 0 {
		t.Errorf("threshold: got %d, want 0 (upstream default)", options.CompactionOptions.Threshold)
	}
}

func TestToCompleteOptions_ToolChoice(t *testing.T) {
	options, err := toCompleteOptions(MessageRequest{
		Tools:      []ToolParam{{Name: "get_weather", InputSchema: map[string]any{"type": "object"}}},
		ToolChoice: &ToolChoice{Type: "tool", Name: "get_weather", DisableParallelToolUse: true},
	})
	if err != nil {
		t.Fatalf("toCompleteOptions: %v", err)
	}

	if options.ToolOptions == nil {
		t.Fatal("expected tool options")
	}
	if options.ToolOptions.Choice != provider.ToolChoiceAny {
		t.Errorf("choice: got %q", options.ToolOptions.Choice)
	}
	if len(options.ToolOptions.Allowed) != 1 || options.ToolOptions.Allowed[0] != "get_weather" {
		t.Errorf("allowed: got %v", options.ToolOptions.Allowed)
	}
	if !options.ToolOptions.DisableParallelToolCalls {
		t.Error("expected parallel tool calls disabled")
	}
}

func TestValidateMessageRequest_MaxTokens(t *testing.T) {
	zero := 0
	negative := -1
	positive := 32

	tests := []struct {
		name    string
		req     MessageRequest
		wantErr bool
	}{
		{name: "missing", req: MessageRequest{}, wantErr: true},
		{name: "negative", req: MessageRequest{MaxTokens: &negative}, wantErr: true},
		{name: "positive", req: MessageRequest{MaxTokens: &positive}},
		{name: "zero prewarm", req: MessageRequest{MaxTokens: &zero}},
		{name: "zero streaming", req: MessageRequest{MaxTokens: &zero, Stream: true}, wantErr: true},
		{
			name:    "zero thinking",
			req:     MessageRequest{MaxTokens: &zero, Thinking: &ThinkingConfig{Type: "enabled", BudgetTokens: 1024}},
			wantErr: true,
		},
		{
			name:    "zero structured output",
			req:     MessageRequest{MaxTokens: &zero, OutputConfig: &OutputConfig{Format: &OutputFormat{Type: "json_schema"}}},
			wantErr: true,
		},
		{
			name:    "zero forced tool",
			req:     MessageRequest{MaxTokens: &zero, ToolChoice: &ToolChoice{Type: "any"}},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMessageRequest(tc.req)
			if tc.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestToCompleteOptions_MaxTokensZero(t *testing.T) {
	zero := 0

	options, err := toCompleteOptions(MessageRequest{MaxTokens: &zero})
	if err != nil {
		t.Fatalf("toCompleteOptions: %v", err)
	}

	if options.MaxTokens == nil || *options.MaxTokens != 0 {
		t.Fatalf("max tokens: got %v, want pointer to 0", options.MaxTokens)
	}
}

func TestMessageRequestDistinguishesMissingAndZeroMaxTokens(t *testing.T) {
	var req MessageRequest
	if err := json.Unmarshal([]byte(`{"max_tokens":0}`), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if req.MaxTokens == nil || *req.MaxTokens != 0 {
		t.Fatalf("max tokens: got %v, want pointer to 0", req.MaxTokens)
	}
}
