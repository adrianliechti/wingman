package config

import (
	"os"
	"path/filepath"
	"testing"
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
	if got := nova.Defaults().Voice; got != "matthew" {
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
