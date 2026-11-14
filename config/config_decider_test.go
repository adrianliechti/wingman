package config

import (
	"testing"
)

const testDecisionProviders = `
providers:
  - type: openai
    token: test-key
    models:
      - gpt-6-sol
      - gpt-6-luna
      - text-embedding-3-small
`

func TestDecisionModels(t *testing.T) {
	cfg, err := parseTestConfig(t, testDecisionProviders)
	if err != nil {
		t.Fatal(err)
	}

	// gpt-6-luna is the one model OpenAI's Decisions API accepts.
	for _, id := range []string{"", "gpt-6-luna"} {
		if model, _, err := cfg.Decider(id); err != nil || model != "gpt-6-luna" {
			t.Fatalf("Decider(%q) = %q, %v", id, model, err)
		}
	}
	if _, err := cfg.Completer("gpt-6-luna"); err != nil {
		t.Fatal("decisions replaced completion support:", err)
	}

	for _, id := range []string{"gpt-6-sol", "text-embedding-3-small", "missing"} {
		if _, _, err := cfg.Decider(id); err == nil {
			t.Fatalf("%q serves decisions", id)
		}
	}
}

func TestDecisionDefaultOrder(t *testing.T) {
	for _, tt := range []struct {
		name, config, want string
	}{
		{"first decider in configuration order", testDecisionProviders + `
  - type: typesafe
    models:
      - jev-latest
`, "gpt-6-luna"},
		{"across providers", `
providers:
  - type: openai-compatible
    models:
      chat:
        id: gpt-6-luna
      microsoft-decision-1:
      a-native:
        type: decider
  - type: typesafe
    models:
      - jev-latest
`, "microsoft-decision-1"},
		{"within a provider", `
providers:
  - type: typesafe
    models:
      - z-jev
      - a-jev
`, "z-jev"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseTestConfig(t, tt.config)
			if err != nil {
				t.Fatal(err)
			}
			if model, _, err := cfg.Decider(""); err != nil || model != tt.want {
				t.Fatalf("default = %q, %v; want %q", model, err, tt.want)
			}
		})
	}
}

func TestDecisionModelsRequireConfiguration(t *testing.T) {
	cfg, err := parseTestConfig(t, `
providers:
  - type: openai-compatible
    models:
      - gpt-6-luna
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cfg.Decider(""); err == nil {
		t.Fatal("a completion model serves decisions outside the openai provider")
	}
}
