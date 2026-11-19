package google

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestComplete_HydrogenThinking(t *testing.T) {
	for _, tc := range []struct {
		name   string
		model  string
		effort provider.Effort
		want   string
	}{
		{"default", "gemini-4-hydrogen", "", ""},
		{"low", "gemini-4-hydrogen", provider.EffortLow, "low"},
		{"medium", "gemini-4-hydrogen", provider.EffortMedium, "medium"},
		{"high", "gemini-4-hydrogen", provider.EffortHigh, "high"},
		{"xhigh", "gemini-4-hydrogen", provider.EffortXHigh, "xhigh"},
		{"max alias", "models/gemini-4-hydrogen", provider.EffortMax, "xhigh"},
		{"neon default", "gemini-4-neon", "", ""},
		{"neon minimal", "gemini-4-neon", provider.EffortMinimal, "low"},
		{"neon xhigh", "gemini-4-neon", provider.EffortXHigh, "xhigh"},
		{"neon max alias", "models/gemini-4-neon", provider.EffortMax, "xhigh"},
		{"carbon", "gemini-4-carbon", provider.EffortXHigh, "high"},
		{"argon", "gemini-4-argon", provider.EffortXHigh, "high"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			handler := func(w http.ResponseWriter, r *http.Request) {
				called = true
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				var wire struct {
					Model            string         `json:"model"`
					GenerationConfig map[string]any `json:"generation_config"`
				}
				if err := json.Unmarshal(data, &wire); err != nil {
					t.Fatal(err)
				}
				level, present := wire.GenerationConfig["thinking_level"]
				if tc.want == "" {
					if present {
						t.Errorf("default thinking level should be omitted: %s", data)
					}
				} else if level != tc.want {
					t.Errorf("thinking level = %v, want %s", level, tc.want)
				}
				if strings.HasPrefix(wire.Model, "models/") {
					t.Error("model prefix retained")
				}
				interactionEvents(createdEvent, completedEvent)(w, r)
			}
			c, err := NewCompleter(tc.model, WithToken("test"), WithClient(newTestClient(t, handler)))
			if err != nil {
				t.Fatal(err)
			}
			for _, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("Hello")}, &provider.CompleteOptions{
				ReasoningOptions: &provider.ReasoningOptions{Effort: tc.effort},
			}) {
				if err != nil {
					t.Fatal(err)
				}
			}
			if !called {
				t.Fatal("request was not sent")
			}
		})
	}
}
