package config

import (
	"slices"
	"strings"
	"testing"
)

func TestProviderModelForms(t *testing.T) {
	for _, tc := range []struct{ models, want string }{
		{"{chat: {misspelled: value}}", "misspelled"},
		{"[{id: gpt-6-sol}]", "into string"},
	} {
		_, err := parseTestConfig(t, "providers:\n  - type: openai-compatible\n    models: "+tc.models)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("models %s: got %v, want error containing %q", tc.models, err, tc.want)
		}
	}
	cfg, err := parseTestConfig(t, `
providers:
  - type: openai-compatible
    models:
      alias:
        id: &model gpt-6-sol
  - type: openai-compatible
    models: &ids [*model]
  - type: openai-compatible
    models: *ids
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Completer("gpt-6-sol"); err != nil {
		t.Fatal(err)
	}
}

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

func TestDetectModelTypes(t *testing.T) {
	for _, tt := range []struct {
		provider string
		id       string
		want     []ModelType
	}{
		{"openai", "gpt-transcribe", []ModelType{ModelTypeTranscriber, ModelTypeRealtime}},
		{"openai", "gpt-4o-mini-transcribe", []ModelType{ModelTypeTranscriber, ModelTypeRealtime}},
		{"OpenAI", "whisper-1", []ModelType{ModelTypeTranscriber, ModelTypeRealtime}},
		{"openai", "gpt-live-transcribe", []ModelType{ModelTypeRealtime}},
		{"openai", "gpt-realtime-whisper", []ModelType{ModelTypeRealtime}},
		{"openai", "MAI-Transcribe-2-Streaming", []ModelType{ModelTypeRealtime}},
		{"openai", "gpt-6-luna", []ModelType{ModelTypeCompleter, ModelTypeDecider}},
		{"openai", "gpt-6.2-astra", []ModelType{ModelTypeCompleter}},
		{"openai", "gpt-6.2-sol", []ModelType{ModelTypeCompleter}},
		{"openai", "gpt-6-sol", []ModelType{ModelTypeCompleter}},
		{"openai-compatible", "gpt-6-luna", []ModelType{ModelTypeCompleter}},
		{"openai-compatible", "whisper-large-v3", []ModelType{ModelTypeTranscriber}},
		{"openrouter", "openai/gpt-4o-transcribe", []ModelType{ModelTypeTranscriber}},
		{"openrouter", "thinkingmachines/inkling-2-small", []ModelType{ModelTypeCompleter}},
		{"mistral", "voxtral-mini-transcribe", []ModelType{ModelTypeTranscriber}},
		{"gemini", "gemini-3.5-transcribe", []ModelType{ModelTypeTranscriber}},
		{"gemini", "lyria-3-clip-preview", []ModelType{ModelTypeSynthesizer}},
		{"openai", "unknown-model", nil},
	} {
		t.Run(tt.provider+"/"+tt.id, func(t *testing.T) {
			if got := detectModelTypes(tt.provider, tt.id); !slices.Equal(got, tt.want) {
				t.Fatalf("detectModelTypes(%q, %q) = %v, want %v", tt.provider, tt.id, got, tt.want)
			}
		})
	}
}

func TestModelWithSeveralTypes(t *testing.T) {
	cfg, err := parseTestConfig(t, `
providers:
  - type: openai
    token: test-openai-token
    models:
      whisper-1:
      gpt-transcribe:
      gpt-live-transcribe:
      gpt-realtime-2.1:
      file-only:
        id: gpt-4o-transcribe
        type: transcriber

  - type: openai-compatible
    url: http://localhost:8080/v1
    models:
      scalar:
        id: whisper-large-v3
        type: realtime
      list:
        id: whisper-large-v3
        type: [transcriber, realtime]
      empty:
        id: whisper-large-v3
        type: []
`)
	if err != nil {
		t.Fatal(err)
	}

	transcribers := map[string]bool{
		"whisper-1": true, "gpt-transcribe": true, "gpt-live-transcribe": false, "gpt-realtime-2.1": false,
		"file-only": true, "scalar": false, "list": true, "empty": true,
	}
	realtimes := map[string]bool{
		"whisper-1": true, "gpt-transcribe": true, "gpt-live-transcribe": true, "gpt-realtime-2.1": true,
		"file-only": false, "scalar": true, "list": true, "empty": false,
	}
	for id, want := range transcribers {
		if _, err := cfg.Transcriber(id); (err == nil) != want {
			t.Errorf("Transcriber(%q) registered = %v, want %v", id, err == nil, want)
		}
	}
	for id, want := range realtimes {
		if _, err := cfg.Realtime(id); (err == nil) != want {
			t.Errorf("Realtime(%q) registered = %v, want %v", id, err == nil, want)
		}
	}

	// A model with several types is still listed once.
	listed := 0
	for _, model := range cfg.Models() {
		if model.ID == "gpt-transcribe" {
			listed++
		}
	}
	if listed != 1 {
		t.Errorf("gpt-transcribe is listed %d times", listed)
	}

	// Defaults are unaffected: the first file transcriber, and a conversation
	// model rather than the transcription models registered before it.
	if transcriber, _ := cfg.Transcriber(""); transcriber == nil {
		t.Fatal("no default transcriber")
	} else if want, _ := cfg.Transcriber("whisper-1"); transcriber != want {
		t.Error("default transcriber is not whisper-1")
	}
	if realtime, _ := cfg.Realtime(""); realtime == nil {
		t.Fatal("no default realtime model")
	} else if want, _ := cfg.Realtime("gpt-realtime-2.1"); realtime != want {
		t.Error("default realtime model is not gpt-realtime-2.1")
	}
}

func TestModelTypeKeepsModelToOneAPI(t *testing.T) {
	cfg, err := parseTestConfig(t, `
providers:
  - type: openai
    token: test-openai-token
    models:
      gpt-6-luna:
        type: completer
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Completer("gpt-6-luna"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cfg.Decider("gpt-6-luna"); err == nil {
		t.Fatal("an explicit completer was also registered as a decider")
	}
}

func TestModelTypeListServesBothAPIs(t *testing.T) {
	cfg, err := parseTestConfig(t, `
providers:
  - type: openai-compatible
    models:
      both:
        id: gpt-6-luna
        type: [completer, decider]
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Completer("both"); err != nil {
		t.Fatal(err)
	}
	if model, _, err := cfg.Decider(""); err != nil || model != "both" {
		t.Fatalf("default decider = %q, %v", model, err)
	}
}

func TestModelTypeListRejectsUnknownType(t *testing.T) {
	_, err := parseTestConfig(t, `
providers:
  - type: openai
    token: test-openai-token
    models:
      gpt-transcribe:
        type: [transcriber, streaming]
`)
	if err == nil || !strings.Contains(err.Error(), "invalid model type: gpt-transcribe") {
		t.Fatalf("error = %v, want an invalid model type", err)
	}
}
