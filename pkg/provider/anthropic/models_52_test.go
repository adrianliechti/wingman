package anthropic

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// Fable/Mythos 5.2 share Fable 5.1's request surface: adaptive thinking is
// always on, forced tool choice is rejected, and per-message effort updates
// stay positional instead of being lowered to the request.
func TestModels52RequestPolicy(t *testing.T) {
	for _, model := range []string{"claude-fable-5-2", "claude-mythos-5-2"} {
		t.Run(model, func(t *testing.T) {
			c, _ := NewCompleter("http://localhost", model)
			input := []provider.Message{provider.UserMessage("Hi")}

			body := requestBody(t, c, input, nil)
			if body["thinking"] != nil || body["output_config"] != nil || body["max_tokens"] != float64(128000) {
				t.Fatalf("changed model defaults: %v", body)
			}
			for _, field := range []string{"temperature", "top_p", "top_k"} {
				if _, exists := body[field]; exists {
					t.Fatalf("unsupported sampling parameter %s", field)
				}
			}

			body = requestBody(t, c, input, &provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeDisabled, Effort: provider.EffortMax}})
			if body["thinking"] != nil || body["output_config"].(map[string]any)["effort"] != "max" {
				t.Fatalf("always-thinking model must omit thinking and keep effort: %v", body)
			}

			body = requestBody(t, c, input, &provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{IncludeSummary: true}})
			if thinking := body["thinking"].(map[string]any); thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
				t.Fatalf("summary = %v", thinking)
			}

			updates := []provider.Message{
				provider.UserMessage("Plan"),
				provider.AssistantMessage("Plan ready"),
				{Content: []provider.Content{provider.ConfigurationUpdateContent(provider.ConfigurationUpdate{ReasoningEffort: provider.EffortLow})}},
				provider.UserMessage("Summarize"),
			}
			body = requestBody(t, c, updates, &provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, Effort: provider.EffortHigh}})
			if len(body["messages"].([]any)) != 4 || body["output_config"].(map[string]any)["effort"] != "high" {
				t.Fatalf("per-message effort must stay positional: %v", body)
			}
		})
	}
}
