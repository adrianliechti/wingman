package realtime_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/server/openai/realtime"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

// TestOpenAITranslationLiveConformance translates one German utterance to
// English, against OpenAI directly and through Wingman, and compares session
// shape, event families, and fields.
//
// Paid and opt-in, like TestOpenAIRealtimeLiveConformance:
//
//	WINGMAN_REALTIME_LIVE_E2E=1 go test ./server/openai/realtime \
//	  -run TestOpenAITranslationLiveConformance -v
func TestOpenAITranslationLiveConformance(t *testing.T) {
	loadDotenv(t)
	if os.Getenv("WINGMAN_REALTIME_LIVE_E2E") != "1" {
		t.Skip("set WINGMAN_REALTIME_LIVE_E2E=1 to run paid OpenAI Realtime conformance tests")
	}

	settings := liveSettings{
		apiKey:          requiredEnv(t, "OPENAI_API_KEY"),
		baseURL:         envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		realtimeModel:   envOr("OPENAI_REALTIME_TRANSLATE_MODEL", "gpt-realtime-translate"),
		transcribeModel: envOr("OPENAI_REALTIME_TRANSLATE_TRANSCRIBE_MODEL", "gpt-realtime-whisper"),
		ttsModel:        envOr("OPENAI_REALTIME_TTS_MODEL", "gpt-4o-mini-tts"),
		ttsVoice:        envOr("OPENAI_REALTIME_TTS_VOICE", "alloy"),
	}
	audio := loadOrSynthesizeAudio(t, settings, "OPENAI_REALTIME_TRANSLATION_AUDIO_FILE",
		"Guten Morgen zusammen. Die neue Version ist endlich stabil, und wir liefern sie heute Nachmittag aus.")

	directURL, err := url.Parse(openAIWebSocketURL(t, settings.baseURL, settings.realtimeModel))
	if err != nil {
		t.Fatal(err)
	}
	directURL.Path += "/translations"
	direct := exerciseLiveTranslation(t, liveTarget{
		name: "openai", url: directURL.String(),
		headers: http.Header{"Authorization": []string{"Bearer " + settings.apiKey}},
	}, settings, audio)

	upstream, err := openai.NewRealtime(settings.baseURL, settings.realtimeModel, openai.WithToken(settings.apiKey))
	if err != nil {
		t.Fatalf("create OpenAI realtime provider: %v", err)
	}
	cfg := &config.Config{}
	cfg.RegisterRealtime(settings.realtimeModel, upstream)
	router := chi.NewRouter()
	realtime.New(cfg).Attach(router)
	server := httptest.NewServer(router)
	defer server.Close()
	adapted := exerciseLiveTranslation(t, liveTarget{
		name: "wingman-adapter",
		url:  "ws" + strings.TrimPrefix(server.URL, "http") + "/realtime/translations?model=" + url.QueryEscape(settings.realtimeModel),
	}, settings, audio)

	for _, value := range []struct {
		name   string
		events []map[string]any
	}{{"openai", direct}, {"wingman-adapter", adapted}} {
		validateLiveTrace(t, value.name, value.events)

		for eventType, language := range map[string]string{"session.created": "es", "session.updated": "en"} {
			session, _ := value.events[firstLiveEvent(value.events, eventType)]["session"].(map[string]any)
			audio, _ := session["audio"].(map[string]any)
			output, _ := audio["output"].(map[string]any)
			if session["type"] != "translation" || session["model"] != settings.realtimeModel || output["language"] != language {
				t.Errorf("%s %s session = %s", value.name, eventType, compactJSON(session))
			}
		}

		var source, target strings.Builder
		for _, event := range value.events {
			switch event["type"] {
			case "session.input_transcript.delta":
				source.WriteString(event["delta"].(string))
			case "session.output_transcript.delta":
				target.WriteString(event["delta"].(string))
			case "session.output_audio.delta":
				if decoded, err := base64.StdEncoding.DecodeString(event["delta"].(string)); err != nil || len(decoded) == 0 {
					t.Errorf("%s invalid audio delta: %v", value.name, err)
				}
			}
		}
		if !strings.Contains(strings.ToLower(source.String()), "morgen") {
			t.Errorf("%s source transcript = %q, want the German input", value.name, source.String())
		}
		if !strings.Contains(strings.ToLower(target.String()), "version") {
			t.Errorf("%s translation = %q, want English", value.name, target.String())
		}
		if last := value.events[len(value.events)-1]; last["type"] != "session.closed" {
			t.Errorf("%s ended with %v, want session.closed", value.name, last["type"])
		}
	}

	// Same event families, and every field OpenAI sends on them.
	directTypes, adaptedTypes := liveEventTypes(direct), liveEventTypes(adapted)
	slices.Sort(directTypes)
	slices.Sort(adaptedTypes)
	if directTypes, adaptedTypes = slices.Compact(directTypes), slices.Compact(adaptedTypes); !slices.Equal(directTypes, adaptedTypes) {
		t.Errorf("event families differ: openai %v, adapter %v", directTypes, adaptedTypes)
	}
	for _, eventType := range directTypes {
		index := firstLiveEvent(adapted, eventType)
		if index < 0 {
			continue
		}
		for field := range direct[firstLiveEvent(direct, eventType)] {
			if _, ok := adapted[index][field]; !ok {
				t.Errorf("adapter %s lacks field %q", eventType, field)
			}
		}
	}

	if t.Failed() {
		t.Logf("openai trace:\n%s", formatLiveTrace(direct))
		t.Logf("wingman-adapter trace:\n%s", formatLiveTrace(adapted))
	}
}

func exerciseLiveTranslation(t *testing.T, target liveTarget, settings liveSettings, audio []byte) []map[string]any {
	t.Helper()
	conn, response, err := websocket.DefaultDialer.Dial(target.url, target.headers)
	if err != nil {
		if response != nil {
			t.Fatalf("%s websocket handshake: %v (%s)", target.name, err, response.Status)
		}
		t.Fatalf("%s websocket handshake: %v", target.name, err)
	}
	defer conn.Close()

	events := readLiveUntil(t, target.name, conn, 20*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "session.created")
	})

	writeLive(t, target.name, conn, map[string]any{
		"type": "session.update",
		"session": map[string]any{"audio": map[string]any{
			"input":  map[string]any{"transcription": map[string]any{"model": settings.transcribeModel}},
			"output": map[string]any{"language": "en"},
		}},
	})
	events = append(events, readLiveUntil(t, target.name, conn, 20*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "session.updated")
	})...)

	// The stream is continuous: real-time frames, then silence to flush.
	const frameBytes = 9600 // 200 ms at PCM16LE/24 kHz/mono.
	audio = append(slices.Clone(audio), make([]byte, 8*frameBytes)...)
	for frame := range slices.Chunk(audio, frameBytes) {
		writeLive(t, target.name, conn, map[string]any{
			"type": "session.input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(frame),
		})
		time.Sleep(200 * time.Millisecond)
	}

	writeLive(t, target.name, conn, map[string]any{"type": "session.close"})
	return append(events, readLiveUntil(t, target.name, conn, 60*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "session.closed")
	})...)
}
