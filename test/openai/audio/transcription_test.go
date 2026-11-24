package audio_test

import (
	"encoding/base64"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/audio"
)

func TestTranscriptionJSON(t *testing.T) {
	h := openai.New(t)
	m := audio.TranscribeModel()

	openaiResp, wingmanResp := audio.CompareHTTP(t, h, m, nil)

	rules := openai.DefaultTranscriptionResponseRules()
	rules["task"] = harness.FieldIgnore
	rules["language"] = harness.FieldIgnore
	rules["duration"] = harness.FieldIgnore

	harness.CompareStructure(t, "json", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})

	audio.AssertElementKeys(t, "json.languages", openaiResp.Body["languages"], wingmanResp.Body["languages"])
}

func TestTranscriptionText(t *testing.T) {
	h := openai.New(t)
	m := audio.TranscribeModel()

	fields := []harness.MultipartField{{Name: "response_format", Value: "text"}}

	openaiResp, wingmanResp := audio.CompareHTTP(t, h, m, fields)

	for _, tc := range []struct {
		label string
		resp  *harness.RawResponse
	}{
		{"openai", openaiResp},
		{"wingman", wingmanResp},
	} {
		if !strings.HasPrefix(tc.resp.Headers.Get("Content-Type"), "text/plain") {
			t.Errorf("%s content type = %q, want text/plain", tc.label, tc.resp.Headers.Get("Content-Type"))
		}

		if strings.TrimSpace(string(tc.resp.RawBody)) == "" {
			t.Errorf("%s returned an empty transcript", tc.label)
		}

		if strings.HasPrefix(strings.TrimSpace(string(tc.resp.RawBody)), "{") {
			t.Errorf("%s returned JSON for response_format=text: %s", tc.label, tc.resp.RawBody)
		}
	}
}

func TestTranscriptionVerboseJSON(t *testing.T) {
	h := openai.New(t)
	m := audio.WhisperModel()

	fields := []harness.MultipartField{
		{Name: "response_format", Value: "verbose_json"},
		{Name: "timestamp_granularities[]", Value: "segment"},
		{Name: "timestamp_granularities[]", Value: "word"},
	}

	openaiResp, wingmanResp := audio.CompareHTTP(t, h, m, fields)

	rules := openai.DefaultTranscriptionResponseRules()
	harness.CompareStructure(t, "verbose_json", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})

	if task, _ := wingmanResp.Body["task"].(string); task != "transcribe" {
		t.Errorf("task = %q, want %q", task, "transcribe")
	}

	if duration, _ := wingmanResp.Body["duration"].(float64); duration <= 0 {
		t.Errorf("duration = %v, want > 0", wingmanResp.Body["duration"])
	}

	// whisper reports per-segment decoder statistics wingman does not model
	stats := []string{"seek", "tokens", "temperature", "avg_logprob", "compression_ratio", "no_speech_prob"}

	audio.AssertElementKeys(t, "verbose_json.segments", openaiResp.Body["segments"], wingmanResp.Body["segments"], stats...)
	audio.AssertElementKeys(t, "verbose_json.words", openaiResp.Body["words"], wingmanResp.Body["words"])
}

func TestTranscriptionDiarizedJSON(t *testing.T) {
	h := openai.New(t)
	m := audio.DiarizeModel()

	fields := []harness.MultipartField{
		{Name: "response_format", Value: "diarized_json"},
		{Name: "chunking_strategy", Value: "auto"},
	}

	openaiResp, wingmanResp := audio.CompareHTTP(t, h, m, fields)

	rules := openai.DefaultTranscriptionResponseRules()
	harness.CompareStructure(t, "diarized_json", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})

	audio.AssertElementKeys(t, "diarized_json.segments", openaiResp.Body["segments"], wingmanResp.Body["segments"])

	segments, _ := wingmanResp.Body["segments"].([]any)

	if len(segments) == 0 {
		t.Fatal("wingman returned no diarized segments")
	}

	for i, item := range segments {
		segment, ok := item.(map[string]any)

		if !ok {
			t.Fatalf("segment %d is not an object: %v", i, item)
		}

		if kind, _ := segment["type"].(string); kind != "transcript.text.segment" {
			t.Errorf("segment %d type = %q, want %q", i, kind, "transcript.text.segment")
		}

		if speaker, _ := segment["speaker"].(string); speaker == "" {
			t.Errorf("segment %d has no speaker", i)
		}

		if _, ok := segment["id"].(string); !ok {
			t.Errorf("segment %d id = %v, want string", i, segment["id"])
		}
	}
}

var (
	srtCue = regexp.MustCompile(`(?m)^\d+\n\d{2}:\d{2}:\d{2},\d{3} --> \d{2}:\d{2}:\d{2},\d{3}$`)
	vttCue = regexp.MustCompile(`(?m)^\d{2}:\d{2}:\d{2}\.\d{3} --> \d{2}:\d{2}:\d{2}\.\d{3}$`)
)

func TestTranscriptionSubtitles(t *testing.T) {
	h := openai.New(t)
	m := audio.WhisperModel()

	for _, tc := range []struct {
		format string
		cue    *regexp.Regexp
		prefix string
	}{
		{format: "srt", cue: srtCue},
		{format: "vtt", cue: vttCue, prefix: "WEBVTT"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			fields := []harness.MultipartField{{Name: "response_format", Value: tc.format}}

			openaiResp, wingmanResp := audio.CompareHTTP(t, h, m, fields)

			for _, sub := range []struct {
				label string
				body  string
			}{
				{"openai", string(openaiResp.RawBody)},
				{"wingman", string(wingmanResp.RawBody)},
			} {
				if tc.prefix != "" && !strings.HasPrefix(sub.body, tc.prefix) {
					t.Errorf("%s %s output does not start with %q: %q", sub.label, tc.format, tc.prefix, truncate(sub.body))
				}

				if !tc.cue.MatchString(sub.body) {
					t.Errorf("%s %s output has no well-formed cue: %q", sub.label, tc.format, truncate(sub.body))
				}
			}
		})
	}
}

func TestTranscriptionSSE(t *testing.T) {
	h := openai.New(t)
	m := audio.TranscribeModel()

	openaiEvents, wingmanEvents := audio.CompareSSE(t, h, m, nil)

	rules := openai.DefaultTranscriptionSSERules()
	harness.CompareSSEStructureByType(t, openaiEvents, wingmanEvents, rules)

	openaiTypes := audio.EventTypes(openaiEvents)
	wingmanTypes := audio.EventTypes(wingmanEvents)

	for _, want := range []string{"transcript.text.delta", "transcript.text.done"} {
		if !slices.Contains(wingmanTypes, want) {
			t.Errorf("wingman stream is missing %q (got %v, openai %v)", want, wingmanTypes, openaiTypes)
		}
	}

	last := wingmanEvents[len(wingmanEvents)-1]

	if last.Raw != "[DONE]" {
		if kind, _ := last.Data["type"].(string); kind != "transcript.text.done" {
			t.Errorf("wingman stream ends with %q, want the done event or [DONE]", last.Raw)
		}
	}

	if text := streamedText(wingmanEvents); text == "" {
		t.Error("wingman stream produced no transcript text")
	} else if done := doneText(wingmanEvents); done != "" && done != text {
		t.Errorf("streamed deltas %q do not match the done event text %q", text, done)
	}
}

func TestTranscriptionContext(t *testing.T) {
	h := openai.New(t)
	m := audio.TranscribeModel()

	fields := []harness.MultipartField{
		{Name: "prompt", Value: "A short product announcement."},
		{Name: "keywords[]", Value: "Wingman"},
		{Name: "keywords[]", Value: "OpenAI"},
		{Name: "languages[]", Value: "en"},
		{Name: "languages[]", Value: "de"},
	}

	openaiResp, wingmanResp := audio.CompareHTTP(t, h, m, fields)

	rules := openai.DefaultTranscriptionResponseRules()
	rules["task"] = harness.FieldIgnore
	rules["language"] = harness.FieldIgnore
	rules["duration"] = harness.FieldIgnore

	harness.CompareStructure(t, "context", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
}

func TestTranscriptionRejectsUnsupportedFormat(t *testing.T) {
	h := openai.New(t)
	m := audio.TranscribeModel()

	h.RequireConfigured(t, m.Wingman)

	fields := []harness.MultipartField{
		{Name: "model", Value: m.Wingman},
		{Name: "response_format", Value: "yaml"},
	}

	resp, err := h.Client.PostMultipart(t.Context(), h.Wingman, audio.Path, fields, []harness.MultipartFile{
		{Name: "file", Filename: "sample.wav", ContentType: "audio/wav", Content: audio.Sample(t)},
	})

	if err != nil {
		t.Fatalf("wingman request failed: %v", err)
	}

	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 (body %s)", resp.StatusCode, truncate(string(resp.RawBody)))
	}
}

func streamedText(events []*harness.SSEEvent) string {
	var text strings.Builder

	for _, event := range events {
		if kind, _ := event.Data["type"].(string); kind != "transcript.text.delta" {
			continue
		}

		delta, _ := event.Data["delta"].(string)
		text.WriteString(delta)
	}

	return text.String()
}

func doneText(events []*harness.SSEEvent) string {
	for _, event := range events {
		if kind, _ := event.Data["type"].(string); kind != "transcript.text.done" {
			continue
		}

		text, _ := event.Data["text"].(string)

		return text
	}

	return ""
}

func truncate(s string) string {
	if len(s) > 240 {
		return s[:240] + "…"
	}

	return s
}

func TestTranscriptionDiarizedSSE(t *testing.T) {
	h := openai.New(t)
	m := audio.DiarizeModel()

	fields := []harness.MultipartField{
		{Name: "response_format", Value: "diarized_json"},
		{Name: "chunking_strategy", Value: "auto"},
	}

	openaiEvents, wingmanEvents := audio.CompareSSE(t, h, m, fields)

	rules := openai.DefaultTranscriptionSSERules()
	harness.CompareSSEStructureByType(t, openaiEvents, wingmanEvents, rules)

	openaiTypes := audio.EventTypes(openaiEvents)
	wingmanTypes := audio.EventTypes(wingmanEvents)

	for _, want := range []string{"transcript.text.segment", "transcript.text.done"} {
		if !slices.Contains(wingmanTypes, want) {
			t.Errorf("wingman stream is missing %q (got %v, openai %v)", want, wingmanTypes, openaiTypes)
		}
	}

	if done := doneText(wingmanEvents); strings.TrimSpace(done) == "" {
		t.Error("wingman done event carries no transcript text")
	}

	for _, event := range wingmanEvents {
		if kind, _ := event.Data["type"].(string); kind != "transcript.text.segment" {
			continue
		}

		if speaker, _ := event.Data["speaker"].(string); speaker == "" {
			t.Errorf("segment event has no speaker: %s", event.Raw)
		}
	}
}

func TestTranscriptionVerboseJSONSegmentOnly(t *testing.T) {
	h := openai.New(t)
	m := audio.WhisperModel()

	fields := []harness.MultipartField{
		{Name: "response_format", Value: "verbose_json"},
		{Name: "timestamp_granularities[]", Value: "segment"},
	}

	openaiResp, wingmanResp := audio.CompareHTTP(t, h, m, fields)

	if _, ok := openaiResp.Body["words"]; ok {
		t.Skip("openai now returns words for segment-only granularity")
	}

	if _, ok := wingmanResp.Body["words"]; ok {
		t.Errorf("wingman returned words for segment-only granularity: %s", truncate(string(wingmanResp.RawBody)))
	}

	if segments, _ := wingmanResp.Body["segments"].([]any); len(segments) == 0 {
		t.Error("wingman returned no segments")
	}
}

// The fixture is 6.96s, inside the 2-10s window required for speaker
// references, so it doubles as its own reference sample.
func TestTranscriptionKnownSpeakers(t *testing.T) {
	h := openai.New(t)
	m := audio.DiarizeModel()

	reference := "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(audio.Sample(t))

	fields := []harness.MultipartField{
		{Name: "response_format", Value: "diarized_json"},
		{Name: "known_speaker_names[]", Value: "narrator"},
		{Name: "known_speaker_references[]", Value: reference},
	}

	openaiResp, wingmanResp := audio.CompareHTTP(t, h, m, fields)

	rules := openai.DefaultTranscriptionResponseRules()
	harness.CompareStructure(t, "known_speakers", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})

	speakers := map[string]bool{}

	segments, _ := wingmanResp.Body["segments"].([]any)

	if len(segments) == 0 {
		t.Fatal("wingman returned no diarized segments")
	}

	for _, item := range segments {
		segment, _ := item.(map[string]any)
		speaker, _ := segment["speaker"].(string)
		speakers[speaker] = true
	}

	if !speakers["narrator"] {
		t.Errorf("known speaker label was not applied, got speakers %v", slices.Sorted(maps.Keys(speakers)))
	}
}

// Wingman drops context hints the target model rejects, so requests OpenAI
// refuses outright still succeed through the proxy.
func TestTranscriptionUnsupportedContextIsDropped(t *testing.T) {
	h := openai.New(t)

	for _, tc := range []struct {
		name   string
		model  audio.Model
		fields []harness.MultipartField
	}{
		{
			name:  "whisper keywords and language list",
			model: audio.WhisperModel(),
			fields: []harness.MultipartField{
				{Name: "keywords[]", Value: "Wingman"},
				{Name: "languages[]", Value: "en"},
				{Name: "languages[]", Value: "de"},
			},
		},
		{
			name:  "diarize prompt",
			model: audio.DiarizeModel(),
			fields: []harness.MultipartField{
				{Name: "response_format", Value: "diarized_json"},
				{Name: "chunking_strategy", Value: "auto"},
				{Name: "prompt", Value: "A short narration."},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.RequireConfigured(t, tc.model.Wingman)

			reference := audio.Post(t, h, h.OpenAI, tc.model.Reference, tc.fields)

			if reference.StatusCode == http.StatusOK {
				t.Skipf("openai now accepts these hints for %s", tc.model.Reference)
			}

			proxied := audio.Post(t, h, h.Wingman, tc.model.Wingman, tc.fields)

			if proxied.StatusCode != http.StatusOK {
				t.Fatalf("wingman returned %d where it should drop the unsupported hints: %s",
					proxied.StatusCode, truncate(string(proxied.RawBody)))
			}

			if text, _ := proxied.Body["text"].(string); strings.TrimSpace(text) == "" {
				t.Errorf("wingman returned no transcript: %s", truncate(string(proxied.RawBody)))
			}
		})
	}
}
