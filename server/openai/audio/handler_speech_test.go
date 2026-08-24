package audio

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func chunks(parts ...string) func(func(*provider.Synthesis, error) bool) {
	return func(yield func(*provider.Synthesis, error) bool) {
		for _, part := range parts {
			chunk := provider.Synthesis{
				Content:     []byte(part),
				ContentType: "audio/mpeg",
			}

			if !yield(&chunk, nil) {
				return
			}
		}
	}
}

func TestWriteAudioStream(t *testing.T) {
	w := httptest.NewRecorder()
	writeAudioStream(w, chunks("abc", "def"))

	if got := w.Header().Get("Content-Type"); got != "audio/mpeg" {
		t.Errorf("content type = %q, want audio/mpeg", got)
	}

	if got := w.Body.String(); got != "abcdef" {
		t.Errorf("body = %q, want %q", got, "abcdef")
	}
}

func TestWriteAudioStreamError(t *testing.T) {
	seq := func(yield func(*provider.Synthesis, error) bool) {
		yield(nil, errors.New("boom"))
	}

	w := httptest.NewRecorder()
	writeAudioStream(w, seq)

	if w.Code != 502 {
		t.Errorf("status = %d, want 502", w.Code)
	}

	if !strings.Contains(w.Body.String(), "boom") {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestWriteSpeechStream(t *testing.T) {
	w := httptest.NewRecorder()
	writeSpeechStream(w, chunks("abc", "def"))

	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("content type = %q, want text/event-stream", got)
	}

	body := w.Body.String()

	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Errorf("stream does not end with [DONE]: %q", body)
	}

	var audio []byte
	var done bool

	for line := range strings.SplitSeq(body, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")

		if !ok || data == "[DONE]" {
			continue
		}

		var event struct {
			Type  string `json:"type"`
			Audio string `json:"audio"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("event is not JSON: %q", data)
		}

		switch event.Type {
		case "speech.audio.delta":
			decoded, err := base64.StdEncoding.DecodeString(event.Audio)

			if err != nil {
				t.Fatalf("delta audio is not base64: %v", err)
			}

			audio = append(audio, decoded...)

		case "speech.audio.done":
			done = true

		default:
			t.Errorf("unexpected event type %q", event.Type)
		}
	}

	if string(audio) != "abcdef" {
		t.Errorf("decoded audio = %q, want %q", audio, "abcdef")
	}

	if !done {
		t.Error("stream is missing the speech.audio.done event")
	}
}
