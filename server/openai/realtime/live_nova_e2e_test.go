package realtime_test

import (
	"encoding/base64"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/provider/bedrock"
	"github.com/adrianliechti/wingman/server/openai/realtime"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

// TestNovaSonicLiveE2E exercises the real Nova 2 Sonic bidirectional endpoint
// through Wingman's OpenAI-compatible WebSocket. It covers voice input,
// cross-modal text input, audio+transcript output, a function call, and the
// post-tool spoken response.
//
// This test makes paid AWS and (unless fixture paths are supplied) OpenAI TTS
// calls and is therefore opt-in:
//
//	WINGMAN_NOVA_LIVE_E2E=1 go test ./server/openai/realtime \
//	  -run TestNovaSonicLiveE2E -v
//
// Set NOVA_REALTIME_HI_AUDIO_FILE and NOVA_REALTIME_WEATHER_AUDIO_FILE to raw
// PCM16LE/24 kHz/mono or WAV fixtures to avoid the OpenAI TTS calls.
func TestNovaSonicLiveE2E(t *testing.T) {
	loadDotenv(t)
	if os.Getenv("WINGMAN_NOVA_LIVE_E2E") != "1" {
		t.Skip("set WINGMAN_NOVA_LIVE_E2E=1 to run the paid Nova Sonic E2E test")
	}

	settings := liveSettings{
		apiKey:   requiredEnv(t, "OPENAI_API_KEY"),
		baseURL:  envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		ttsModel: envOr("OPENAI_REALTIME_TTS_MODEL", "gpt-4o-mini-tts"),
		ttsVoice: envOr("OPENAI_REALTIME_TTS_VOICE", "alloy"),
	}
	hiAudio := loadOrSynthesizeAudio(t, settings, "NOVA_REALTIME_HI_AUDIO_FILE", "Hi")
	weatherAudio := loadOrSynthesizeAudio(t, settings, "NOVA_REALTIME_WEATHER_AUDIO_FILE", "What's the weather in New York?")

	target, closeAdapter := startNovaAdapter(t)
	defer closeAdapter()

	conn, response, err := websocket.DefaultDialer.Dial(target.url, nil)
	if err != nil {
		if response != nil {
			t.Fatalf("Nova adapter websocket handshake: %v (%s)", err, response.Status)
		}
		t.Fatalf("Nova adapter websocket handshake: %v", err)
	}
	defer conn.Close()

	opening := readLiveUntil(t, target.name, conn, 20*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "session.created")
	})
	writeLive(t, target.name, conn, map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"type": "realtime",
			"instructions": "Answer in one short sentence. Only call get_weather for weather questions. " +
				"After a weather tool result, state the result briefly.",
			"output_modalities": []string{"audio"},
			"tool_choice":       "auto",
			"tools":             []map[string]any{weatherTool()},
			"audio": map[string]any{
				"input": map[string]any{
					"format":        map[string]any{"type": "audio/pcm", "rate": 24000},
					"transcription": map[string]any{"model": "gpt-live-transcribe"},
					"turn_detection": map[string]any{
						"type": "server_vad", "create_response": true, "interrupt_response": true,
					},
				},
				"output": map[string]any{
					"format": map[string]any{"type": "audio/pcm", "rate": 24000},
					"voice":  "alloy",
				},
			},
		},
	})
	opening = append(opening, readLiveUntil(t, target.name, conn, 20*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "session.updated")
	})...)
	validateLiveTrace(t, "Nova opening", opening)

	streamNovaPCM(t, target.name, conn, hiAudio)
	voice := readNovaUntil(t, target.name, conn, 45*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "conversation.item.input_audio_transcription.completed") && hasLiveEvent(events, "response.done")
	})
	validateLiveTrace(t, "Nova voice", voice)
	assertTranscriptContains(t, "Nova voice", voice, "hi")
	assertNovaAudioResponse(t, "Nova voice", voice)

	// Keep the audio channel active before exercising Nova's cross-modal text
	// input. Sonic requires an active streaming session even for typed turns.
	streamNovaSilence(t, target.name, conn, 500*time.Millisecond)
	writeLive(t, target.name, conn, map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type": "message", "role": "user",
			"content": []map[string]any{{"type": "input_text", "text": "Reply with exactly: text turn received"}},
		},
	})
	writeLive(t, target.name, conn, map[string]any{"type": "response.create"})
	textTurn := readNovaUntil(t, target.name, conn, 45*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "response.done")
	})
	validateLiveTrace(t, "Nova text", textTurn)
	assertNovaAudioResponse(t, "Nova text", textTurn)

	streamNovaPCM(t, target.name, conn, weatherAudio)
	toolTurn := readNovaUntil(t, target.name, conn, 45*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "response.function_call_arguments.done") && hasLiveEvent(events, "response.done")
	})
	validateLiveTrace(t, "Nova tool", toolTurn)
	assertTranscriptContains(t, "Nova weather", toolTurn, "weather", "new york")
	assertWeatherToolCall(t, "Nova", toolTurn)
	assertToolPartialOrder(t, "Nova tool", toolTurn)

	callID := toolCallID(t, "Nova", toolTurn)
	writeLive(t, target.name, conn, map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type": "function_call_output", "call_id": callID,
			"output": `{"city":"New York","temperature":72,"unit":"fahrenheit","condition":"sunny"}`,
		},
	})
	writeLive(t, target.name, conn, map[string]any{"type": "response.create"})
	postTool := readNovaUntil(t, target.name, conn, 45*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "response.output_audio.delta") && hasLiveEvent(events, "response.done")
	})
	validateLiveTrace(t, "Nova post-tool", postTool)
	assertNovaAudioResponse(t, "Nova post-tool", postTool)
	if transcript := strings.ToLower(outputTranscript(postTool)); !strings.Contains(transcript, "72") && !strings.Contains(transcript, "seventy-two") {
		t.Errorf("Nova post-tool transcript %q does not contain the supplied temperature", transcript)
	}
}

func startNovaAdapter(t *testing.T) (liveTarget, func()) {
	t.Helper()
	modelID := envOr("NOVA_REALTIME_MODEL_ID", "amazon.nova-2-sonic-v1:0")
	region := envOr("NOVA_REALTIME_REGION", "eu-north-1")
	upstream, err := bedrock.NewRealtime(modelID, bedrock.WithRegion(region))
	if err != nil {
		t.Fatalf("create Nova realtime provider: %v", err)
	}

	const alias = "nova-live-e2e"
	cfg := &config.Config{}
	cfg.RegisterRealtime(alias, upstream)
	router := chi.NewRouter()
	realtime.New(cfg).Attach(router)
	server := httptest.NewServer(router)
	target := "ws" + strings.TrimPrefix(server.URL, "http") + "/realtime?model=" + url.QueryEscape(alias)
	return liveTarget{name: "nova-adapter", url: target}, server.Close
}

func streamNovaPCM(t *testing.T, name string, conn *websocket.Conn, audio []byte) {
	t.Helper()
	const frameBytes = 1536 // 32 ms at PCM16LE/24 kHz/mono.
	for offset := 0; offset < len(audio); offset += frameBytes {
		end := min(offset+frameBytes, len(audio))
		writeLive(t, name, conn, map[string]any{
			"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(audio[offset:end]),
		})
		time.Sleep(32 * time.Millisecond)
	}
	streamNovaSilence(t, name, conn, 2*time.Second)
}

func streamNovaSilence(t *testing.T, name string, conn *websocket.Conn, duration time.Duration) {
	t.Helper()
	const frameBytes = 1536
	encoded := base64.StdEncoding.EncodeToString(make([]byte, frameBytes))
	for elapsed := time.Duration(0); elapsed < duration; elapsed += 32 * time.Millisecond {
		writeLive(t, name, conn, map[string]any{"type": "input_audio_buffer.append", "audio": encoded})
		time.Sleep(32 * time.Millisecond)
	}
}

// Nova Sonic expects audioInput events to remain continuous for the lifetime
// of an interactive session, including while it is speaking. Browsers naturally
// do this because the microphone worklet keeps producing frames. Keep the live
// test faithful to that behavior while it waits for an output turn to finish.
func readNovaUntil(
	t *testing.T,
	name string,
	conn *websocket.Conn,
	timeout time.Duration,
	done func([]map[string]any) bool,
) []map[string]any {
	t.Helper()

	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		const frameBytes = 1536
		encoded := base64.StdEncoding.EncodeToString(make([]byte, frameBytes))
		ticker := time.NewTicker(32 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if err := conn.WriteJSON(map[string]any{
					"type": "input_audio_buffer.append", "audio": encoded,
				}); err != nil {
					return
				}
			}
		}
	}()

	events := readLiveUntil(t, name, conn, timeout, done)
	close(stop)
	<-stopped
	return events
}

func assertNovaAudioResponse(t *testing.T, label string, events []map[string]any) {
	t.Helper()
	for _, eventType := range []string{
		"response.created", "response.output_audio.delta", "response.output_audio.done",
		"response.output_audio_transcript.done", "response.output_item.done", "response.done",
	} {
		if !hasLiveEvent(events, eventType) {
			t.Errorf("%s missing %s; types=%v", label, eventType, liveEventTypes(events))
		}
	}
	assertPartialOrder(t, label, events, audioResponseOrder())
}

func outputTranscript(events []map[string]any) string {
	var transcript string
	for _, event := range events {
		switch event["type"] {
		case "response.output_audio_transcript.delta":
			transcript += event["delta"].(string)
		case "response.output_audio_transcript.done":
			if value, ok := event["transcript"].(string); ok && value != "" {
				transcript = value
			}
		}
	}
	return transcript
}
