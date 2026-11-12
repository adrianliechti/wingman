package google

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func newTestTranscriber(t *testing.T, handler http.HandlerFunc) *Transcriber {
	t.Helper()

	tr, err := NewTranscriber("gemini-3.5-transcribe", WithToken("test-token"), WithClient(newTestClient(t, handler)))
	if err != nil {
		t.Fatal(err)
	}

	return tr
}

// utterance builds an audioTranscription part the way the transcribe model
// returns it: words as protobuf durations, and the text repeated in Text
// whenever metadata is present.
func utterance(text, speaker string, words ...[3]string) map[string]any {
	transcription := map[string]any{"text": text}

	if speaker != "" {
		transcription["speakerLabel"] = speaker
	}

	if len(words) > 0 {
		var list []any

		for _, w := range words {
			list = append(list, map[string]any{"word": w[0], "startOffset": w[1], "endOffset": w[2]})
		}

		transcription["words"] = list
	}

	part := map[string]any{"audioTranscription": transcription}

	if speaker != "" || len(words) > 0 {
		part["text"] = text
	}

	return part
}

func sseTranscription(parts ...map[string]any) string {
	body := map[string]any{
		"responseId": "resp_stt",
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role":  "model",
					"parts": parts,
				},
			},
		},
	}

	raw, _ := json.Marshal(body)

	return "data: " + string(raw) + "\n\n"
}

// oneSecondWAV is a silent 24 kHz mono 16-bit second, so the duration
// fallback is checkable.
var oneSecondWAV = encodeWAV(make([]byte, 48000), defaultAudioFormat)

func transcribe(t *testing.T, tr *Transcriber, input provider.File, options *provider.TranscribeOptions) ([]*provider.Transcription, provider.Transcription, error) {
	t.Helper()

	var (
		deltas []*provider.Transcription
		acc    provider.TranscriptionAccumulator
	)

	for delta, err := range tr.Transcribe(context.Background(), input, options) {
		if err != nil {
			return deltas, acc.Result(), err
		}

		deltas = append(deltas, delta)
		acc.Add(*delta)
	}

	return deltas, acc.Result(), nil
}

func TestTranscribe_RequestShape(t *testing.T) {
	var gotPath string
	var gotBody map[string]any

	tr := newTestTranscriber(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.UnmarshalRead(r.Body, &gotBody)

		serveSSE(sseTranscription(utterance("Hello.", "spk:0", [3]string{"Hello.", "0.100s", "0.500s"})))(w, r)
	})

	input := provider.File{Name: "sample.wav", ContentType: "audio/wav", Content: oneSecondWAV}

	_, _, err := transcribe(t, tr, input, &provider.TranscribeOptions{
		Instructions: "A short narration.",
		Languages:    []string{"en"},
		Keywords:     []string{"Wingman"},
		Diarize:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasSuffix(gotPath, "/models/gemini-3.5-transcribe:streamGenerateContent") {
		t.Errorf("expected a streamGenerateContent call, got %q", gotPath)
	}

	contents, _ := gotBody["contents"].([]any)
	content, _ := contents[0].(map[string]any)
	parts, _ := content["parts"].([]any)

	if len(parts) != 2 {
		t.Fatalf("expected an audio part and a prompt part, got %v", parts)
	}

	audio, _ := parts[0].(map[string]any)
	inline, _ := audio["inlineData"].(map[string]any)

	if inline["mimeType"] != "audio/wav" || inline["data"] != base64.StdEncoding.EncodeToString(oneSecondWAV) {
		t.Errorf("expected the upload as inline audio/wav, got %v", audio)
	}

	prompt, _ := parts[1].(map[string]any)

	if prompt["text"] != "A short narration." {
		t.Errorf("expected the instructions as a text part, got %v", prompt)
	}

	if gotBody["store"] != false {
		t.Errorf("expected request logging to be disabled, got store=%v", gotBody["store"])
	}

	generation, _ := gotBody["generationConfig"].(map[string]any)
	config, _ := generation["audioTranscriptionConfig"].(map[string]any)

	if config["diarization"] != true || config["wordTimestamp"] != true {
		t.Errorf("expected diarization with word timestamps, got %v", config)
	}

	if languages, _ := config["languageCodes"].([]any); len(languages) != 1 || languages[0] != "en" {
		t.Errorf("expected languageCodes [en], got %v", config["languageCodes"])
	}

	if _, ok := config["customVocabulary"]; ok {
		t.Errorf("custom vocabulary must be dropped next to diarization, got %v", config)
	}
}

func TestTranscribe_KeywordsWithoutTimestamps(t *testing.T) {
	var gotBody map[string]any

	tr := newTestTranscriber(t, func(w http.ResponseWriter, r *http.Request) {
		json.UnmarshalRead(r.Body, &gotBody)

		serveSSE(sseTranscription(utterance("Hello Wingman.", "")))(w, r)
	})

	_, _, err := transcribe(t, tr, provider.File{Name: "sample.mp3"}, &provider.TranscribeOptions{Keywords: []string{"Wingman"}})
	if err != nil {
		t.Fatal(err)
	}

	generation, _ := gotBody["generationConfig"].(map[string]any)
	config, _ := generation["audioTranscriptionConfig"].(map[string]any)

	if vocabulary, _ := config["customVocabulary"].([]any); len(vocabulary) != 1 || vocabulary[0] != "Wingman" {
		t.Errorf("expected keywords as custom vocabulary, got %v", config)
	}

	if _, ok := config["diarization"]; ok {
		t.Errorf("expected no diarization, got %v", config)
	}

	if _, ok := config["wordTimestamp"]; ok {
		t.Errorf("expected no word timestamps, got %v", config)
	}
}

func TestTranscribe_PlainText(t *testing.T) {
	tr := newTestTranscriber(t, serveSSE(
		sseTranscription(utterance("The sun rises in the east.", "")),
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}]}`+"\n\n",
	))

	input := provider.File{Name: "sample.wav", ContentType: "audio/wav", Content: oneSecondWAV}

	deltas, result, err := transcribe(t, tr, input, nil)
	if err != nil {
		t.Fatal(err)
	}

	if result.Text != "The sun rises in the east." {
		t.Errorf("expected the transcript, got %q", result.Text)
	}

	if result.ID != "resp_stt" || result.Model != "gemini-3.5-transcribe" {
		t.Errorf("expected response id and model, got %+v", result)
	}

	if len(result.Segments) != 0 || len(result.Words) != 0 {
		t.Errorf("expected no structure without timestamps, got %+v", result)
	}

	if result.Duration != 1 {
		t.Errorf("expected the WAV duration of 1s as fallback, got %v", result.Duration)
	}

	if last := deltas[len(deltas)-1]; last.Text != "" || last.Duration != 1 {
		t.Errorf("expected a trailing delta carrying only the duration, got %+v", last)
	}
}

func TestTranscribe_DiarizedSegments(t *testing.T) {
	tr := newTestTranscriber(t, serveSSE(sseTranscription(
		utterance("Hello there.", "spk:0", [3]string{"Hello", "0.100s", "0.400s"}, [3]string{"there.", "0.400s", "0.900s"}),
		utterance("Hi.", "spk:1", [3]string{"Hi.", "1.200s", "1.500s"}),
		utterance("Bye.", "spk:0", [3]string{"Bye.", "2s", "2.300s"}),
	)))

	input := provider.File{Name: "sample.wav", ContentType: "audio/wav", Content: oneSecondWAV}

	_, result, err := transcribe(t, tr, input, &provider.TranscribeOptions{
		Diarize:  true,
		Speakers: []provider.TranscribeSpeaker{{Name: "narrator"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if result.Text != "Hello there. Hi. Bye." {
		t.Errorf("expected utterances joined by spaces, got %q", result.Text)
	}

	if len(result.Segments) != 3 {
		t.Fatalf("expected one segment per utterance, got %+v", result.Segments)
	}

	want := []provider.TranscriptionSegment{
		{ID: "0", Speaker: "narrator", Start: 0.1, End: 0.9, Text: "Hello there."},
		{ID: "1", Speaker: "B", Start: 1.2, End: 1.5, Text: "Hi."},
		{ID: "2", Speaker: "narrator", Start: 2, End: 2.3, Text: "Bye."},
	}

	for i, segment := range result.Segments {
		if segment != want[i] {
			t.Errorf("segment %d = %+v, want %+v", i, segment, want[i])
		}
	}

	if len(result.Words) != 4 || result.Words[1].Word != "there." || result.Words[1].End != 0.9 {
		t.Errorf("expected the words with parsed offsets, got %+v", result.Words)
	}

	if result.Duration != 2.3 {
		t.Errorf("expected the duration from the last word, got %v", result.Duration)
	}
}

func TestTranscribe_StreamedUtterances(t *testing.T) {
	tr := newTestTranscriber(t, serveSSE(
		sseTranscription(utterance("Hello world.", "", [3]string{"Hello", "0s", "0.300s"}, [3]string{"world.", "0.300s", "0.800s"})),
		sseTranscription(utterance("Goodbye.", "", [3]string{"Goodbye.", "1s", "1.400s"})),
	))

	deltas, result, err := transcribe(t, tr, provider.File{Name: "sample.mp3"}, &provider.TranscribeOptions{Timestamps: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(deltas) != 3 {
		t.Fatalf("expected two utterance deltas and a trailing one, got %d", len(deltas))
	}

	if deltas[0].Text != "Hello world." || deltas[1].Text != " Goodbye." {
		t.Errorf("expected the second delta to carry its separator, got %q and %q", deltas[0].Text, deltas[1].Text)
	}

	if result.Text != "Hello world. Goodbye." {
		t.Errorf("expected the accumulated transcript, got %q", result.Text)
	}

	if len(result.Segments) != 2 || result.Segments[1].ID != "1" || result.Segments[1].Speaker != "" {
		t.Errorf("expected numbered segments without speakers, got %+v", result.Segments)
	}
}

func TestTranscribe_UtteranceOffsets(t *testing.T) {
	tr := newTestTranscriber(t, serveSSE(
		`data: {"candidates":[{"content":{"role":"model","parts":[{"audioTranscription":{"text":"First line.","speakerLabel":"spk:0","startOffset":"0.200s","endOffset":"1.500s"}},{"audioTranscription":{"text":"Second line.","speakerLabel":"spk:1","startOffset":"1.800s","endOffset":"3.100s"}}]},"finishReason":"STOP"}]}`+"\n\n",
	))

	_, result, err := transcribe(t, tr, provider.File{Name: "sample.mp3"}, &provider.TranscribeOptions{Diarize: true})
	if err != nil {
		t.Fatal(err)
	}

	want := []provider.TranscriptionSegment{
		{ID: "0", Speaker: "A", Start: 0.2, End: 1.5, Text: "First line."},
		{ID: "1", Speaker: "B", Start: 1.8, End: 3.1, Text: "Second line."},
	}

	if len(result.Segments) != len(want) {
		t.Fatalf("segments = %+v, want %+v", result.Segments, want)
	}

	for i := range want {
		if result.Segments[i] != want[i] {
			t.Errorf("segment %d = %+v, want %+v", i, result.Segments[i], want[i])
		}
	}

	if result.Duration != 3.1 {
		t.Errorf("duration = %v, want 3.1", result.Duration)
	}
}

func TestTranscribe_APIError(t *testing.T) {
	tr := newTestTranscriber(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"code":400,"message":"custom_vocabulary is incompatible with diarization.","status":"INVALID_ARGUMENT"}}`)
	})

	_, _, err := transcribe(t, tr, provider.File{Name: "sample.mp3"}, nil)

	var providerErr *provider.ProviderError

	if !errors.As(err, &providerErr) || providerErr.Code != http.StatusBadRequest {
		t.Fatalf("expected a 400 provider error, got %v", err)
	}
}

func TestAudioMIMEType(t *testing.T) {
	tests := []struct {
		file provider.File
		want string
	}{
		{provider.File{ContentType: "audio/mpeg"}, "audio/mpeg"},
		{provider.File{ContentType: "audio/x-wav; charset=binary"}, "audio/wav"},
		{provider.File{ContentType: "audio/mp4"}, "audio/m4a"},
		{provider.File{ContentType: "application/octet-stream", Name: "clip.MP3"}, "audio/mp3"},
		{provider.File{Name: "clip.weba"}, "audio/webm"},
		{provider.File{Name: "clip", Content: []byte("RIFF....WAVE")}, "audio/wav"},
		{provider.File{Name: "clip", Content: []byte("fLaC")}, "audio/flac"},
		{provider.File{Name: "clip", Content: []byte{0xFF, 0xFB, 0x90}}, "audio/mp3"},
		{provider.File{}, "audio/mp3"},
	}

	for _, tt := range tests {
		if got := audioMIMEType(tt.file); got != tt.want {
			t.Errorf("audioMIMEType(%q, %q) = %q, want %q", tt.file.ContentType, tt.file.Name, got, tt.want)
		}
	}
}

func TestParseOffset(t *testing.T) {
	tests := map[string]float64{
		"":       0,
		"0.100s": 0.1,
		"2s":     2,
		"1m2s":   62,
		"bogus":  0,
	}

	for value, want := range tests {
		if got := parseOffset(value); got != want {
			t.Errorf("parseOffset(%q) = %v, want %v", value, got, want)
		}
	}
}
