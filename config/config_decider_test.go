package config

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/decider"

	"go.yaml.in/yaml/v4"
)

const testDecisionProviders = `
providers:
  - type: openai
    token: test-key
    models:
      - gpt-6-luna
      - gpt-6-sol
      - text-embedding-3-small
`

func TestDecisionAliasesReuseModels(t *testing.T) {
	cfg, err := parseTestConfig(t, testDecisionProviders+`
deciders:
  gpt-6-luna:
    model: gpt-6-luna
  vectors:
    model: text-embedding-3-small
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Completer("gpt-6-luna"); err != nil {
		t.Fatal("decision alias lost completion support:", err)
	}
	if _, err := cfg.Embedder("text-embedding-3-small"); err != nil {
		t.Fatal("decision alias lost embedding support:", err)
	}
	for _, id := range []string{"", "gpt-6-luna"} {
		model, p, err := cfg.Decider(id)
		if err != nil || model != "gpt-6-luna" {
			t.Fatalf("Decider(%q) = %q, %v", id, model, err)
		}
		if _, ok := p.(*decider.CompleterAdapter); !ok {
			t.Fatalf("Decider(%q) = %T, want completion adapter", id, p)
		}
	}
	if model, p, err := cfg.Decider("vectors"); err != nil || model != "vectors" {
		t.Fatalf("embedding alias = %q, %v", model, err)
	} else if _, ok := p.(*decider.EmbedderAdapter); !ok {
		t.Fatalf("embedding alias = %T, want embedding adapter", p)
	}
	for _, id := range []string{"gpt-6-sol", "text-embedding-3-small", "missing"} {
		if _, _, err := cfg.Decider(id); err == nil {
			t.Fatalf("enabled decisions without an alias for %q", id)
		}
	}
	if _, err := cfg.Completer("vectors"); err == nil {
		t.Fatal("decision-only alias registered as a completer")
	}
	if _, err := cfg.Embedder("vectors"); err == nil {
		t.Fatal("decision-only alias registered as an embedder")
	}
	if len(cfg.Models()) != 4 {
		t.Fatalf("model aliases missing or duplicated: %+v", cfg.Models())
	}
}

func TestDecisionDefaultOrder(t *testing.T) {
	for _, tt := range []struct {
		name, extra, want string
	}{
		{"first adapter", `
deciders:
  z-vectors: {model: text-embedding-3-small}
  a-chat: {model: gpt-6-luna}
`, "z-vectors"},
		{"first native across providers", `
  - type: openai-compatible
    models:
      microsoft-decision-1:
        id: microsoft-decision-1
      a-native:
        type: decider
  - type: typesafe
    models:
      - jev-latest
deciders:
  gpt-6-luna: {model: gpt-6-luna}
`, "microsoft-decision-1"},
		{"first typesafe native", `
  - type: typesafe
    models:
      - z-jev
      - a-jev
deciders:
  gpt-6-luna: {model: gpt-6-luna}
`, "z-jev"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseTestConfig(t, testDecisionProviders+tt.extra)
			if err != nil {
				t.Fatal(err)
			}
			model, _, err := cfg.Decider("")
			if err != nil || model != tt.want {
				t.Fatalf("default = %q, %v; want %q", model, err, tt.want)
			}
		})
	}
}

func TestDecisionModelsRequireOptIn(t *testing.T) {
	cfg, err := parseTestConfig(t, testDecisionProviders)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "gpt-6-luna", "text-embedding-3-small"} {
		if _, _, err := cfg.Decider(id); err == nil {
			t.Fatalf("enabled decisions without opt-in for %q", id)
		}
	}
}

func TestDecisionAliasValidation(t *testing.T) {
	for _, tt := range []struct{ name, extra, want string }{
		{"missing model", "deciders:\n  chat: {}\n", "requires an alias and model"},
		{"empty alias", "deciders:\n  '': {model: gpt-6-luna}\n", "requires an alias and model"},
		{"unknown model", "deciders:\n  chat: {model: missing}\n", "configured completer or embedder: missing"},
		{"unknown field", "deciders:\n  chat: {model: gpt-6-luna, typo: true}\n", "typo"},
		{"invalid effort", "deciders:\n  chat: {model: gpt-6-luna, effort: turbo}\n", "invalid decider effort: turbo"},
		{"embedding effort", "deciders:\n  vectors: {model: text-embedding-3-small, effort: low}\n", "effort requires a completion-backed decider"},
		{"native collision", `
  - type: typesafe
    models: [native]
deciders:
  native: {model: gpt-6-luna}
`, "decision alias already registered: native"},
		{"native adapter source", `
  - type: typesafe
    models: [native]
deciders:
  chat: {model: native}
`, "configured completer or embedder: native"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseTestConfig(t, testDecisionProviders+tt.extra)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

type decisionOptionsCompleter struct {
	options *provider.CompleteOptions
}

func (p *decisionOptionsCompleter) Complete(_ context.Context, _ []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	p.options = options
	return func(yield func(*provider.Completion, error) bool) {
		message := provider.AssistantMessage(`{"q0":{"0":1,"1":0}}`)
		yield(&provider.Completion{Message: &message}, nil)
	}
}

func TestDecisionAliasEffort(t *testing.T) {
	backend := &decisionOptionsCompleter{}
	cfg := &Config{}
	cfg.RegisterCompleter("chat", backend)
	var file configFile
	if err := yaml.Load([]byte(`
deciders:
  chat:
    model: chat
    effort: high
`), &file); err != nil {
		t.Fatal(err)
	}
	if err := cfg.registerDeciders(&file); err != nil {
		t.Fatal(err)
	}
	_, p, err := cfg.Decider("chat")
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Decide(t.Context(), &provider.DecisionInput{
		State: "hello", Questions: []provider.DecisionQuestion{{ID: "q", Noul: &provider.NoulQuestion{}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if options := backend.options; options == nil || options.ReasoningOptions == nil ||
		options.ReasoningOptions.Type != provider.ReasoningTypeAdaptive || options.ReasoningOptions.Effort != provider.EffortHigh {
		t.Fatalf("configured effort did not reach completion: %+v", options)
	}
	if backend.options.OutputOptions != nil {
		t.Fatal("decision adapter should leave verbosity unset")
	}
}
