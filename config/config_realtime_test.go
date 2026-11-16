package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman/pkg/budget"
)

func TestRealtimeProviderConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
providers:
  - type: openai
    token: test-openai-token
    models:
      - gpt-realtime-2.1
      - gpt-realtime-2.1-mini
      - gpt-live-transcribe
      - gpt-transcribe

  - type: gemini
    token: test-gemini-token
    models:
      - gemini-3.1-flash-live-preview
      - gemini-3.5-transcribe-live

  - type: gemini
    token: test-gemini-token
    vars:
      affective_dialog: "true"
    models:
      - gemini-2.5-flash-native-audio-preview-12-2025

  - type: bedrock
    vars:
      region: us-east-1
    models:
      nova-2-sonic:
        id: amazon.nova-2-sonic-v1:0
        type: realtime
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse(path)
	if err != nil {
		t.Fatalf("parse realtime provider example: %v", err)
	}

	for _, model := range []string{
		"gpt-realtime-2.1",
		"gpt-realtime-2.1-mini",
		"gpt-live-transcribe",
		"gemini-3.1-flash-live-preview",
		"gemini-3.5-transcribe-live",
		"gemini-2.5-flash-native-audio-preview-12-2025",
		"nova-2-sonic",
	} {
		realtime, err := cfg.Realtime(model)
		if err != nil {
			t.Errorf("Realtime(%q): %v", model, err)
			continue
		}
		if realtime == nil {
			t.Errorf("Realtime(%q) returned nil", model)
		}
	}

	// gpt-live-transcribe is a realtime-only companion model; it must never
	// be reachable as a file-transcription target (explicit or default) —
	// only gpt-transcribe should resolve there.
	if _, err := cfg.Transcriber("gpt-live-transcribe"); err == nil {
		t.Error("Transcriber(\"gpt-live-transcribe\") should not resolve; it is a realtime-only model")
	}

	if _, err := cfg.Transcriber("gpt-transcribe"); err != nil {
		t.Errorf("Transcriber(%q): %v", "gpt-transcribe", err)
	}

	transcriber, err := cfg.Transcriber("")
	if err != nil {
		t.Fatalf("Transcriber(\"\") default: %v", err)
	}
	if want, err := cfg.Transcriber("gpt-transcribe"); err != nil || transcriber != want {
		t.Error("default transcriber should be gpt-transcribe, not the realtime-only gpt-live-transcribe")
	}

	nova, err := cfg.Realtime("nova-2-sonic")
	if err != nil {
		t.Fatal(err)
	}
	if got := nova.Defaults().Output.Voice; got != "matthew" {
		t.Errorf("Nova default voice = %q, want matthew", got)
	}
}

func TestDetectRealtimeAndTranscriptionModels(t *testing.T) {
	tests := map[string]ModelType{
		"gpt-realtime-2.1":                              ModelTypeRealtime,
		"gpt-realtime-2.1-mini":                         ModelTypeRealtime,
		"gemini-3.1-flash-live-preview":                 ModelTypeRealtime,
		"gemini-2.5-flash-native-audio-preview-12-2025": ModelTypeRealtime,
		"gemini-3.5-transcribe-live":                    ModelTypeRealtime,
		"gemini-3.8-live":                               ModelTypeRealtime,
		"gemini-3.8-live-extended-thinking":             ModelTypeRealtime,
		"gemini-3.8-flash":                              ModelTypeCompleter,
		"gemini-nano-banana-2.1":                        ModelTypeRenderer,
		"amazon.nova-2-sonic-v1:0":                      ModelTypeRealtime,
		"gpt-live-transcribe":                           ModelTypeRealtime,
		"gpt-transcribe":                                ModelTypeTranscriber,
	}
	for model, want := range tests {
		t.Run(model, func(t *testing.T) {
			if got := DetectModelType(model); got != want {
				t.Errorf("DetectModelType(%q) = %q, want %q", model, got, want)
			}
		})
	}
}

func TestRealtimeDefaultSkipsLiveTranscribers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
providers:
  - type: openai
    token: test-openai-token
    models:
      - gpt-live-transcribe
      - gpt-realtime-translate
      - gpt-realtime-2.1
      - gpt-realtime-2.1-mini
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}

	realtime, err := cfg.Realtime("")
	if err != nil {
		t.Fatalf("Realtime(\"\") default: %v", err)
	}
	if want, err := cfg.Realtime("gpt-realtime-2.1"); err != nil || realtime != want {
		t.Error("default realtime should be gpt-realtime-2.1, not the transcription-only gpt-live-transcribe")
	}
}

func TestRealtimeDefaultUsesProviderCapabilitiesThroughAliases(t *testing.T) {
	const dedicated = `
providers:
  - type: openai
    token: test
    models:
      speech: {id: whisper-1}
      dictation: {id: gpt-live-transcribe}
      interpreter: {id: gpt-realtime-translate}
  - type: gemini
    token: test
    models:
      captions: {id: gemini-3.5-transcribe-live}
`
	cfg, err := parseTestConfig(t, dedicated)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Realtime(""); err == nil {
		t.Fatal("dedicated models supplied a default conversation")
	}
	for _, tc := range []struct{ provider, model string }{
		{"openai", "gpt-realtime-2.1"},
		{"gemini", "gemini-3.8-live"},
		{"bedrock", "amazon.nova-2-sonic-v1:0"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			// This conversational alias deliberately contains a formerly excluded word.
			cfg, err := parseTestConfig(t, dedicated+fmt.Sprintf(`
  - type: %s
    token: test
    vars: {region: us-east-1}
    models:
      translation-chat: {id: %s, type: realtime}
`, tc.provider, tc.model))
			if err != nil {
				t.Fatal(err)
			}
			got, err := cfg.Realtime("")
			if err != nil {
				t.Fatal(err)
			}
			want, err := cfg.Realtime("translation-chat")
			if err != nil || got != want {
				t.Fatal("default did not select the conversational provider")
			}

			wrapped := &Config{}
			for _, alias := range []string{"speech", "dictation", "interpreter", "captions", "translation-chat"} {
				p, err := cfg.Realtime(alias)
				if err != nil {
					t.Fatal(err)
				}
				wrapped.RegisterRealtime(alias, budget.NewRealtime(tc.provider, alias, p, nil))
			}
			got, err = wrapped.Realtime("")
			want, _ = wrapped.Realtime("translation-chat")
			if err != nil || got != want {
				t.Fatal("budget wrapping lost the model's capabilities")
			}
		})
	}
}
