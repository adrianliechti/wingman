package audio_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/audio"
)

const speechPath = "/audio/speech"

func speechBody(model, streamFormat string) map[string]any {
	body := map[string]any{
		"model": model,
		"voice": "marin",
		"input": "The sun rises in the east.",
	}

	if streamFormat != "" {
		body["stream_format"] = streamFormat
	}

	return body
}

func TestSpeechAudio(t *testing.T) {
	h := openai.New(t)
	m := audio.SpeechModel()

	h.RequireConfigured(t, m.Wingman)

	ctx := context.Background()

	reference, err := h.Client.PostRaw(ctx, h.OpenAI, speechPath, speechBody(m.Reference, ""))

	if err != nil {
		t.Fatalf("openai request failed: %v", err)
	}

	if reference.StatusCode != http.StatusOK {
		t.Skipf("openai returned %d", reference.StatusCode)
	}

	proxied, err := h.Client.PostRaw(ctx, h.Wingman, speechPath, speechBody(m.Wingman, ""))

	if err != nil {
		t.Fatalf("wingman request failed: %v", err)
	}

	if proxied.StatusCode != http.StatusOK {
		t.Fatalf("wingman returned %d: %s", proxied.StatusCode, summarize(proxied))
	}

	if got, want := proxied.Headers.Get("Content-Type"), reference.Headers.Get("Content-Type"); got != want {
		t.Errorf("content type = %q, want %q", got, want)
	}

	if len(proxied.RawBody) == 0 {
		t.Fatal("wingman returned no audio")
	}

	// mp3 frames start with an ID3 tag or a frame sync
	if !isMP3(proxied.RawBody) {
		t.Errorf("wingman body does not look like mp3: % x", proxied.RawBody[:min(8, len(proxied.RawBody))])
	}

	if !isMP3(reference.RawBody) {
		t.Errorf("openai body does not look like mp3: % x", reference.RawBody[:min(8, len(reference.RawBody))])
	}
}

func TestSpeechSSE(t *testing.T) {
	h := openai.New(t)
	m := audio.SpeechModel()

	h.RequireConfigured(t, m.Wingman)

	ctx := context.Background()

	referenceEvents, err := h.Client.PostSSE(ctx, h.OpenAI, speechPath, speechBody(m.Reference, "sse"))

	if err != nil {
		t.Skipf("openai stream request failed: %v", err)
	}

	proxiedEvents, err := h.Client.PostSSE(ctx, h.Wingman, speechPath, speechBody(m.Wingman, "sse"))

	if err != nil {
		t.Fatalf("wingman stream request failed: %v", err)
	}

	referenceTypes := audio.EventTypes(referenceEvents)
	proxiedTypes := audio.EventTypes(proxiedEvents)

	for _, want := range referenceTypes {
		if !slices.Contains(proxiedTypes, want) {
			t.Errorf("wingman stream is missing %q (got %v, openai %v)", want, proxiedTypes, referenceTypes)
		}
	}

	payload := decodeSpeechStream(t, proxiedEvents)

	if len(payload) == 0 {
		t.Fatal("wingman stream carried no audio")
	}

	if !isMP3(payload) {
		t.Errorf("reassembled audio does not look like mp3: % x", payload[:min(8, len(payload))])
	}

	if reference := decodeSpeechStream(t, referenceEvents); len(reference) == 0 {
		t.Error("openai stream carried no audio")
	}
}

func decodeSpeechStream(t *testing.T, events []*harness.SSEEvent) []byte {
	t.Helper()

	var audio []byte

	for _, event := range events {
		if kind, _ := event.Data["type"].(string); kind != "speech.audio.delta" {
			continue
		}

		encoded, _ := event.Data["audio"].(string)

		decoded, err := base64.StdEncoding.DecodeString(encoded)

		if err != nil {
			t.Fatalf("delta audio is not base64: %v", err)
		}

		audio = append(audio, decoded...)
	}

	return audio
}

func TestSpeechRejectsUnsupportedStreamFormat(t *testing.T) {
	h := openai.New(t)
	m := audio.SpeechModel()

	h.RequireConfigured(t, m.Wingman)

	resp, err := h.Client.PostRaw(context.Background(), h.Wingman, speechPath, speechBody(m.Wingman, "flac"))

	if err != nil {
		t.Fatalf("wingman request failed: %v", err)
	}

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// summarize keeps binary payloads out of test output.
func summarize(resp *harness.RawResponse) string {
	if resp.Body != nil {
		return string(resp.RawBody)
	}

	return fmt.Sprintf("%d bytes of %s", len(resp.RawBody), resp.Headers.Get("Content-Type"))
}

func isMP3(data []byte) bool {
	if len(data) < 3 {
		return false
	}

	if strings.HasPrefix(string(data), "ID3") {
		return true
	}

	return data[0] == 0xFF && data[1]&0xE0 == 0xE0
}
