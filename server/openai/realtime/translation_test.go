package realtime

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

func newFakeTranslator() *fakeRealtime {
	translator := newFakeRealtime()
	translator.capabilities.TranslationOnly = true
	translator.defaults = &provider.RealtimeOptions{OutputLanguage: "es"}
	return translator
}

func openTestTranslation(t *testing.T, translator provider.Realtime) *websocket.Conn {
	t.Helper()
	cfg := &config.Config{}
	cfg.RegisterRealtime("test-translate", translator)
	router := chi.NewRouter()
	New(cfg).Attach(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/realtime/translations?model=test-translate", nil)
	if err != nil {
		t.Fatalf("dial realtime translation: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func translationUpdate(audio map[string]any) map[string]any {
	return map[string]any{"type": "session.update", "session": map[string]any{"audio": audio}}
}

func TestTranslationSession(t *testing.T) {
	translator := newFakeTranslator()
	conn := openTestTranslation(t, translator)

	created := readWireEvent(t, conn)
	assertEventType(t, created, "session.created")
	session := created["session"].(map[string]any)
	if session["type"] != "translation" || session["model"] != "test-translate" {
		t.Fatalf("session = %#v, want a translation session", session)
	}
	audio := session["audio"].(map[string]any)
	if language := audio["output"].(map[string]any)["language"]; language != "es" {
		t.Errorf("initial language = %v, want the translator's default", language)
	}
	if input := audio["input"].(map[string]any); len(input) != 2 || input["transcription"] != nil || input["noise_reduction"] != nil {
		t.Errorf("initial input = %#v", input)
	}

	// No conversation.created: the update is acknowledged next.
	writeWireEvent(t, conn, translationUpdate(map[string]any{
		"input":  map[string]any{"transcription": map[string]any{"model": "gpt-realtime-whisper"}},
		"output": map[string]any{"language": "en"},
	}))
	updated := readWireEvent(t, conn)
	assertEventType(t, updated, "session.updated")
	audio = updated["session"].(map[string]any)["audio"].(map[string]any)
	if audio["output"].(map[string]any)["language"] != "en" || audio["input"].(map[string]any)["transcription"].(map[string]any)["model"] != "gpt-realtime-whisper" {
		t.Fatalf("updated audio = %#v", audio)
	}

	writeWireEvent(t, conn, map[string]any{
		"type": "session.input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(make([]byte, 9600)),
	})
	waitSignal(t, translator.session.audioSent, "streamed source audio")
	translator.mu.Lock()
	options := translator.options[0]
	translator.mu.Unlock()
	if options.OutputLanguage != "en" || options.InputTranscription == nil || options.InputTranscription.Model != "gpt-realtime-whisper" {
		t.Fatalf("provider options = %#v", options)
	}

	translator.session.emit(
		provider.RealtimeEvent{Type: provider.RealtimeEventAudioDelta, Audio: []byte{1, 2, 3, 4}, AudioEnd: 200 * time.Millisecond},
		provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, Role: provider.MessageRoleUser, Text: " Guten", AudioEnd: 800 * time.Millisecond},
		provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, Role: provider.MessageRoleAssistant, Text: "Good", AudioEnd: 1200 * time.Millisecond},
	)
	output := readWireEvent(t, conn)
	assertEventType(t, output, "session.output_audio.delta")
	if output["delta"] != "AQIDBA==" || output["format"] != "pcm16" || output["sample_rate"] != float64(24000) || output["channels"] != float64(1) || output["elapsed_ms"] != float64(200) {
		t.Fatalf("audio delta = %#v", output)
	}
	source := readWireEvent(t, conn)
	assertEventType(t, source, "session.input_transcript.delta")
	if source["delta"] != " Guten" || source["elapsed_ms"] != float64(800) {
		t.Fatalf("input transcript = %#v", source)
	}
	target := readWireEvent(t, conn)
	assertEventType(t, target, "session.output_transcript.delta")
	if target["delta"] != "Good" || target["elapsed_ms"] != float64(1200) {
		t.Fatalf("output transcript = %#v", target)
	}

	writeWireEvent(t, conn, translationUpdate(map[string]any{"output": map[string]any{"language": "fr"}}))
	waitSignal(t, translator.session.updated, "language change")
	assertEventType(t, readWireEvent(t, conn), "session.updated")

	// Closing flushes the provider; the session ends once it is done.
	writeWireEvent(t, conn, map[string]any{"type": "session.close"})
	waitSignal(t, translator.session.committed, "translation flush")
	translator.session.emit(
		provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, Role: provider.MessageRoleAssistant, Text: " morning"},
		provider.RealtimeEvent{Type: provider.RealtimeEventResponseDone},
	)
	assertEventType(t, readWireEvent(t, conn), "session.output_transcript.delta")
	assertEventType(t, readWireEvent(t, conn), "session.closed")
	if _, _, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		t.Fatalf("read after session.closed = %v, want a normal close", err)
	}
}

func TestTranslationSessionRejections(t *testing.T) {
	conn := openTestTranslation(t, newFakeTranslator())
	assertEventType(t, readWireEvent(t, conn), "session.created")

	for _, tc := range []struct {
		event map[string]any
		want  string
	}{
		{map[string]any{"type": "input_audio_buffer.append", "audio": "AAAA"}, "unsupported client event type"},
		{map[string]any{"type": "response.create"}, "unsupported client event type"},
		{map[string]any{"type": "session.update", "session": map[string]any{"type": "translation"}}, `session field "/type" is not supported`},
		{translationUpdate(map[string]any{"output": map[string]any{"voice": "alloy"}}), `session field "/audio/output/voice" is not supported`},
		{translationUpdate(map[string]any{"input": map[string]any{"transcription": map[string]any{}}}), "must be null or an object with only a model"},
		{translationUpdate(map[string]any{"input": map[string]any{"transcription": map[string]any{"model": "gpt-realtime-whisper", "language": "de"}}}), "must be null or an object with only a model"},
	} {
		writeWireEvent(t, conn, tc.event)
		reply := readWireEvent(t, conn)
		assertEventType(t, reply, "error")
		if message, _ := reply["error"].(map[string]any)["message"].(string); !strings.Contains(message, tc.want) {
			t.Errorf("%v error = %q, want %q", tc.event["type"], message, tc.want)
		}
	}

	// Nothing was translated, so closing ends the session right away.
	writeWireEvent(t, conn, map[string]any{"type": "session.close"})
	assertEventType(t, readWireEvent(t, conn), "session.closed")
}

func TestTranslationRequiresTranslator(t *testing.T) {
	cfg := &config.Config{}
	cfg.RegisterRealtime("test-realtime", newFakeRealtime())

	request := httptest.NewRequest(http.MethodGet, "/realtime/translations?model=test-realtime", nil)
	response := httptest.NewRecorder()
	New(cfg).handleRealtime(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "does not translate") {
		t.Fatalf("response = %d %q, want 400 for a model that does not translate", response.Code, response.Body.String())
	}
}
