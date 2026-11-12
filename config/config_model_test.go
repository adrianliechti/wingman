package config

import "testing"

func TestDetectDecisionModels(t *testing.T) {
	for _, tt := range []struct {
		id   string
		want ModelType
	}{
		{"microsoft-decision-1", ModelTypeDecider},
		{"gemini-decision-1", ModelTypeDecider},
		{"MICROSOFT-DECISION-1", ModelTypeDecider},
		{"custom-decider", ModelTypeDecider},
		{"typesafe-jev", ModelTypeDecider},
		{"typesafe/jev-1.13", ModelTypeDecider},
		{"jev-latest", ModelTypeDecider},
		{"gpt-6-luna", ModelTypeCompleter},
		{"text-embedding-3-small", ModelTypeEmbedder},
		{"unknown-model", ModelTypeAuto},
	} {
		t.Run(tt.id, func(t *testing.T) {
			if got := DetectModelType(tt.id); got != tt.want {
				t.Fatalf("DetectModelType(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestDecisionModelTypeOverride(t *testing.T) {
	cfg, err := parseTestConfig(t, `
providers:
  - type: openai-compatible
    models:
      native:
        id: microsoft-decision-1
      chat:
        id: microsoft-decision-1
        type: completer
`)
	if err != nil {
		t.Fatal(err)
	}
	if model, _, err := cfg.Decider(""); err != nil || model != "native" {
		t.Fatalf("inferred native decider = %q, %v", model, err)
	}
	if _, err := cfg.Completer("chat"); err != nil {
		t.Fatal("explicit type did not override detection:", err)
	}
	if _, _, err := cfg.Decider("chat"); err == nil {
		t.Fatal("explicit completer was also registered as a decider")
	}
}
