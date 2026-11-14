package realtime_test

import (
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

// TestOpenAITranscriptionLiveConformance dictates one utterance into a
// transcription-only session, against OpenAI directly and through Wingman,
// and compares session shape, event families, and lifecycle order.
//
// Paid and opt-in, like TestOpenAIRealtimeLiveConformance:
//
//	WINGMAN_REALTIME_LIVE_E2E=1 go test ./server/openai/realtime \
//	  -run TestOpenAITranscriptionLiveConformance -v
func TestOpenAITranscriptionLiveConformance(t *testing.T) {
	loadDotenv(t)
	if os.Getenv("WINGMAN_REALTIME_LIVE_E2E") != "1" {
		t.Skip("set WINGMAN_REALTIME_LIVE_E2E=1 to run paid OpenAI Realtime conformance tests")
	}

	settings := liveSettings{
		apiKey:          requiredEnv(t, "OPENAI_API_KEY"),
		baseURL:         envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		transcribeModel: envOr("OPENAI_REALTIME_TRANSCRIBE_MODEL", "gpt-live-transcribe"),
		ttsModel:        envOr("OPENAI_REALTIME_TTS_MODEL", "gpt-4o-mini-tts"),
		ttsVoice:        envOr("OPENAI_REALTIME_TTS_VOICE", "alloy"),
	}
	audio := loadOrSynthesizeAudio(t, settings, "OPENAI_REALTIME_DICTATION_AUDIO_FILE",
		"The deployment pipeline is finally green again, so we can ship the release this afternoon.")

	directURL, err := url.Parse(openAIWebSocketURL(t, settings.baseURL, ""))
	if err != nil {
		t.Fatal(err)
	}
	directURL.RawQuery = "intent=transcription"
	direct := exerciseLiveTranscription(t, liveTarget{
		name: "openai", url: directURL.String(),
		headers: http.Header{"Authorization": []string{"Bearer " + settings.apiKey}},
	}, settings, audio)

	upstream, err := openai.NewRealtime(settings.baseURL, settings.transcribeModel, openai.WithToken(settings.apiKey))
	if err != nil {
		t.Fatalf("create OpenAI realtime provider: %v", err)
	}
	cfg := &config.Config{}
	cfg.RegisterRealtime(settings.transcribeModel, upstream)
	router := chi.NewRouter()
	realtime.New(cfg).Attach(router)
	server := httptest.NewServer(router)
	defer server.Close()
	adapted := exerciseLiveTranscription(t, liveTarget{
		name: "wingman-adapter", url: "ws" + strings.TrimPrefix(server.URL, "http") + "/realtime?intent=transcription",
	}, settings, audio)

	for _, value := range []struct {
		name  string
		trace liveTranscriptionTrace
	}{{"openai", direct}, {"wingman-adapter", adapted}} {
		validateLiveTrace(t, value.name+" opening", value.trace.opening)
		validateLiveTrace(t, value.name+" dictation", value.trace.dictation)

		for _, eventType := range []string{"session.created", "session.updated"} {
			session, _ := value.trace.opening[firstLiveEvent(value.trace.opening, eventType)]["session"].(map[string]any)
			if session["type"] != "transcription" || session["object"] != "realtime.transcription_session" {
				t.Errorf("%s %s is not a transcription session: %s", value.name, eventType, compactJSON(session))
			}
			audio, _ := session["audio"].(map[string]any)
			input, _ := audio["input"].(map[string]any)
			for _, field := range []string{"format", "noise_reduction", "transcription", "turn_detection"} {
				if _, ok := input[field]; !ok {
					t.Errorf("%s %s lacks session.audio.input.%s", value.name, eventType, field)
				}
			}
			if _, ok := audio["output"]; ok {
				t.Errorf("%s %s exposes audio output", value.name, eventType)
			}
			if eventType == "session.created" {
				continue
			}
			transcription, _ := input["transcription"].(map[string]any)
			languages, _ := transcription["languages"].([]any)
			if transcription["model"] != settings.transcribeModel || len(languages) != 1 || languages[0] != "en" {
				t.Errorf("%s session.updated transcription = %s", value.name, compactJSON(transcription))
			}
			if input["turn_detection"] != nil {
				t.Errorf("%s session.updated turn_detection = %s, want null", value.name, compactJSON(input["turn_detection"]))
			}
		}
		if hasLiveEvent(value.trace.opening, "conversation.created") {
			t.Errorf("%s transcription session emitted conversation.created", value.name)
		}

		// Deltas stream before the commit, which announces the item.
		assertPartialOrder(t, value.name+" dictation", value.trace.dictation, [][2]string{
			{"conversation.item.input_audio_transcription.delta", "input_audio_buffer.committed"},
			{"input_audio_buffer.committed", "conversation.item.added"},
			{"conversation.item.added", "conversation.item.done"},
			{"conversation.item.done", "conversation.item.input_audio_transcription.completed"},
		})
		var itemIDs []string
		for _, event := range value.trace.dictation {
			switch event["type"] {
			case "conversation.item.input_audio_transcription.delta", "input_audio_buffer.committed",
				"conversation.item.input_audio_transcription.completed":
				if id, _ := event["item_id"].(string); !slices.Contains(itemIDs, id) {
					itemIDs = append(itemIDs, id)
				}
			}
		}
		if len(itemIDs) != 1 || itemIDs[0] == "" {
			t.Errorf("%s dictation spans item ids %v, want one", value.name, itemIDs)
		}
		assertTranscriptContains(t, value.name+" dictation", value.trace.dictation, "pipeline")
		completed := value.trace.dictation[firstLiveEvent(value.trace.dictation, "conversation.item.input_audio_transcription.completed")]
		if transcript, _ := completed["transcript"].(string); transcript != strings.TrimSpace(transcript) {
			t.Errorf("%s final transcript %q is padded", value.name, transcript)
		}

		failure, _ := value.trace.rejected["error"].(map[string]any)
		if value.trace.rejected["type"] != "error" || failure["code"] != "response_create_for_transcription_session" {
			t.Errorf("%s response.create reply = %s", value.name, compactJSON(value.trace.rejected))
		}
	}

	compareLiveTurn(t, "streamed dictation", direct.dictation, adapted.dictation, []string{
		"conversation.item.input_audio_transcription.delta",
		"input_audio_buffer.committed",
		"conversation.item.added",
		"conversation.item.done",
		"conversation.item.input_audio_transcription.completed",
	})
	for _, eventType := range liveEventTypes(adapted.dictation) {
		// conversation.item.created is the legacy alias Wingman keeps emitting.
		if eventType != "conversation.item.created" && !hasLiveEvent(direct.dictation, eventType) {
			t.Errorf("adapter emitted %s, which OpenAI does not send for a dictation", eventType)
		}
	}

	if t.Failed() {
		t.Logf("openai opening trace:\n%s", formatLiveTrace(direct.opening))
		t.Logf("wingman-adapter opening trace:\n%s", formatLiveTrace(adapted.opening))
		t.Logf("openai dictation trace:\n%s", formatLiveTrace(direct.dictation))
		t.Logf("wingman-adapter dictation trace:\n%s", formatLiveTrace(adapted.dictation))
	}
}

type liveTranscriptionTrace struct {
	opening   []map[string]any
	dictation []map[string]any
	rejected  map[string]any
}

func exerciseLiveTranscription(t *testing.T, target liveTarget, settings liveSettings, audio []byte) liveTranscriptionTrace {
	t.Helper()
	conn, response, err := websocket.DefaultDialer.Dial(target.url, target.headers)
	if err != nil {
		if response != nil {
			t.Fatalf("%s websocket handshake: %v (%s)", target.name, err, response.Status)
		}
		t.Fatalf("%s websocket handshake: %v", target.name, err)
	}
	defer conn.Close()

	trace := liveTranscriptionTrace{}
	trace.opening = readLiveUntil(t, target.name, conn, 20*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "session.created")
	})

	writeLive(t, target.name, conn, map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"type": "transcription",
			"audio": map[string]any{
				"input": map[string]any{
					"format": map[string]any{"type": "audio/pcm", "rate": 24000},
					"transcription": map[string]any{
						"model": settings.transcribeModel, "languages": []string{"en"}, "delay": "low",
					},
					"turn_detection": nil,
				},
			},
		},
	})
	trace.opening = append(trace.opening, readLiveUntil(t, target.name, conn, 20*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "session.updated")
	})...)

	// Real-time pacing lets deltas arrive before the commit.
	streamLivePCM(t, target.name, conn, audio)
	writeLive(t, target.name, conn, map[string]any{"type": "input_audio_buffer.commit"})
	trace.dictation = readLiveUntil(t, target.name, conn, 60*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "conversation.item.input_audio_transcription.completed")
	})

	writeLive(t, target.name, conn, map[string]any{"type": "response.create"})
	if err := conn.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := conn.ReadJSON(&trace.rejected); err != nil {
		t.Fatalf("%s read response.create reply: %v", target.name, err)
	}
	return trace
}
