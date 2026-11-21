package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSynthesizerProviderConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
providers:
  - type: gemini
    token: test-gemini-token
    models:
      - gemini-3.8-flash-tts
      - gemini-3.8-flash-lite-tts
      - lyria-3.5

  - type: google
    token: test-gemini-token
    models:
      gemini-tts:
        id: gemini-2.5-flash-preview-tts
        type: synthesizer
      music:
        id: models/lyria-3.5
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse(path)
	if err != nil {
		t.Fatalf("parse synthesizer provider example: %v", err)
	}

	for _, model := range []string{
		"gemini-3.8-flash-tts",
		"gemini-3.8-flash-lite-tts",
		"gemini-tts",
		"lyria-3.5",
		"music",
	} {
		synthesizer, err := cfg.Synthesizer(model)
		if err != nil {
			t.Fatalf("synthesizer %q: %v", model, err)
		}

		if synthesizer == nil {
			t.Errorf("synthesizer %q: expected an adapter, got nil", model)
		}
	}
}
