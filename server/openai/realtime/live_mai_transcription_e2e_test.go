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

// TestMAITranscriptionLiveConformance dictates one utterance into
// MAI-Transcribe-2-Streaming on Microsoft Foundry, directly and through
// Wingman. The client leaves turn detection unset, so Wingman must leave it
// to the service, which only finalizes on commit.
//
// Paid and opt-in:
//
//	WINGMAN_MAI_LIVE_E2E=1 go test ./server/openai/realtime \
//	  -run TestMAITranscriptionLiveConformance -v
//
// AZURE_MAI_ENDPOINT is the resource root (https://<resource>.services.ai.azure.com),
// AZURE_MAI_API_KEY its key, and AZURE_MAI_DEPLOYMENT_NAME the deployment
// (default MAI-Transcribe-2-Streaming). The dictation is synthesized with
// OpenAI TTS (OPENAI_API_KEY) unless MAI_REALTIME_DICTATION_AUDIO_FILE names a
// PCM16LE/24 kHz/mono or WAV fixture.
func TestMAITranscriptionLiveConformance(t *testing.T) {
	loadDotenv(t)
	if os.Getenv("WINGMAN_MAI_LIVE_E2E") != "1" {
		t.Skip("set WINGMAN_MAI_LIVE_E2E=1 to run paid MAI-Transcribe conformance tests")
	}

	endpoint, err := url.Parse(strings.TrimRight(requiredEnv(t, "AZURE_MAI_ENDPOINT"), "/"))
	if err != nil || endpoint.Host == "" {
		t.Fatalf("AZURE_MAI_ENDPOINT must be the resource root URL: %v", err)
	}
	key := requiredEnv(t, "AZURE_MAI_API_KEY")
	deployment := envOr("AZURE_MAI_DEPLOYMENT_NAME", "MAI-Transcribe-2-Streaming")

	audio := loadOrSynthesizeAudio(t, liveSettings{
		apiKey:   os.Getenv("OPENAI_API_KEY"),
		baseURL:  envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		ttsModel: envOr("OPENAI_REALTIME_TTS_MODEL", "gpt-4o-mini-tts"),
		ttsVoice: envOr("OPENAI_REALTIME_TTS_VOICE", "alloy"),
	}, "MAI_REALTIME_DICTATION_AUDIO_FILE",
		"The deployment pipeline is finally green again, so we can ship the release this afternoon.")

	direct := exerciseMAITranscription(t, liveTarget{
		name:    "mai",
		url:     "wss://" + endpoint.Host + "/mai/v1/realtime?intent=transcription",
		headers: http.Header{"api-key": []string{key}},
	}, deployment, audio)

	upstream, err := openai.NewRealtime("https://"+endpoint.Host+"/mai/v1", deployment, openai.WithToken(key))
	if err != nil {
		t.Fatalf("create MAI realtime provider: %v", err)
	}
	cfg := &config.Config{}
	cfg.RegisterRealtime(deployment, upstream)
	router := chi.NewRouter()
	realtime.New(cfg).Attach(router)
	server := httptest.NewServer(router)
	defer server.Close()
	adapted := exerciseMAITranscription(t, liveTarget{
		name: "wingman-adapter", url: "ws" + strings.TrimPrefix(server.URL, "http") + "/realtime?intent=transcription",
	}, deployment, audio)

	for _, value := range []struct {
		name  string
		trace liveTranscriptionTrace
	}{{"mai", direct}, {"wingman-adapter", adapted}} {
		assertTranscriptContains(t, value.name+" dictation", value.trace.dictation, "pipeline")
	}

	// MAI's own events need not follow OpenAI's envelope; Wingman's must.
	validateLiveTrace(t, "wingman-adapter opening", adapted.opening)
	validateLiveTrace(t, "wingman-adapter dictation", adapted.dictation)
	assertPartialOrder(t, "wingman-adapter dictation", adapted.dictation, [][2]string{
		{"conversation.item.input_audio_transcription.delta", "conversation.item.input_audio_transcription.completed"},
		{"input_audio_buffer.committed", "conversation.item.input_audio_transcription.completed"},
	})

	// One dictation is one item, and its deltas add up to the final transcript.
	var itemIDs []string
	var deltas strings.Builder
	var transcript string
	for _, event := range adapted.dictation {
		switch event["type"] {
		case "conversation.item.input_audio_transcription.delta", "input_audio_buffer.committed",
			"conversation.item.input_audio_transcription.completed":
			if id, _ := event["item_id"].(string); !slices.Contains(itemIDs, id) {
				itemIDs = append(itemIDs, id)
			}
		}
		switch event["type"] {
		case "conversation.item.input_audio_transcription.delta":
			delta, _ := event["delta"].(string)
			deltas.WriteString(delta)
		case "conversation.item.input_audio_transcription.completed":
			transcript, _ = event["transcript"].(string)
		}
	}
	if len(itemIDs) != 1 || itemIDs[0] == "" {
		t.Errorf("adapter dictation spans item ids %v, want one", itemIDs)
	}
	if got := strings.TrimSpace(deltas.String()); got != strings.TrimSpace(transcript) {
		t.Errorf("adapter deltas %q differ from the final transcript %q", got, transcript)
	}

	if t.Failed() || testing.Verbose() {
		t.Logf("mai opening trace:\n%s", formatLiveTrace(direct.opening))
		t.Logf("wingman-adapter opening trace:\n%s", formatLiveTrace(adapted.opening))
		t.Logf("mai dictation trace:\n%s", formatLiveTrace(direct.dictation))
		t.Logf("wingman-adapter dictation trace:\n%s", formatLiveTrace(adapted.dictation))
	}
}

func exerciseMAITranscription(t *testing.T, target liveTarget, deployment string, audio []byte) liveTranscriptionTrace {
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

	// No turn_detection: the service decides, as a typical client expects.
	writeLive(t, target.name, conn, map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"type": "transcription",
			"audio": map[string]any{
				"input": map[string]any{
					"format":        map[string]any{"type": "audio/pcm", "rate": 24000},
					"transcription": map[string]any{"model": deployment, "language": "en"},
				},
			},
		},
	})
	trace.opening = append(trace.opening, readLiveUntil(t, target.name, conn, 20*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "session.updated")
	})...)

	// MAI has no server VAD; only the commit finalizes the item.
	streamLivePCM(t, target.name, conn, audio)
	writeLive(t, target.name, conn, map[string]any{"type": "input_audio_buffer.commit"})
	trace.dictation = readLiveUntil(t, target.name, conn, 60*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "conversation.item.input_audio_transcription.completed")
	})
	return trace
}
