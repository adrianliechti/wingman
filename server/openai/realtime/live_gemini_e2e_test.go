package realtime_test

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/server/openai/realtime"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

// TestGeminiRealtimeLiveE2E exercises real Gemini sessions through Wingman's
// local OpenAI-compatible WebSocket handler. It covers both 3.8 Live models,
// manual/automatic activity detection, and repeated text-to-audio turns.
//
// This test makes paid Gemini API calls and is opt-in:
//
//	WINGMAN_GEMINI_REALTIME_LIVE_E2E=1 go test ./server/openai/realtime \
//	  -run TestGeminiRealtimeLiveE2E -count=1 -v
//
// Set GEMINI_REALTIME_AUDIO_FILE to a PCM16LE/24 kHz/mono or WAV fixture to
// also exercise speech input. No TTS calls are made by this test.
func TestGeminiRealtimeLiveE2E(t *testing.T) {
	if os.Getenv("WINGMAN_GEMINI_REALTIME_LIVE_E2E") != "1" {
		t.Skip("set WINGMAN_GEMINI_REALTIME_LIVE_E2E=1 to run paid Gemini Realtime tests")
	}
	loadDotenv(t)
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		key = requiredEnv(t, "GOOGLE_API_KEY")
	}
	var audio []byte
	if path := os.Getenv("GEMINI_REALTIME_AUDIO_FILE"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		audio = normalizePCMFixture(t, path, data)
	}

	for _, model := range []string{"gemini-3.8-live", "gemini-3.8-live-extended-thinking"} {
		t.Run(model, func(t *testing.T) {
			backend, err := google.NewRealtime("", model, google.WithToken(key))
			if err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{}
			cfg.RegisterRealtime(model, backend)
			router := chi.NewRouter()
			router.Route("/v1", realtime.New(cfg).Attach)
			server := httptest.NewServer(router)
			defer server.Close()

			for _, mode := range []string{"manual", "automatic"} {
				t.Run(mode, func(t *testing.T) {
					conn, _, err := websocket.DefaultDialer.Dial(openAIWebSocketURL(t, server.URL+"/v1", model), nil)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					readLiveUntil(t, model, conn, 10*time.Second, func(events []map[string]any) bool {
						return hasLiveEvent(events, "session.created")
					})
					var turnDetection any
					if mode == "automatic" {
						turnDetection = map[string]any{"type": "server_vad", "create_response": true, "interrupt_response": true}
					}
					writeLive(t, model, conn, map[string]any{
						"type": "session.update", "session": map[string]any{
							"type": "realtime", "output_modalities": []string{"audio"},
							"instructions": "Answer every request in one short spoken sentence. Use get_weather for weather questions.",
							"tools":        []map[string]any{weatherTool()},
							"audio": map[string]any{
								"input": map[string]any{
									"format":        map[string]any{"type": "audio/pcm", "rate": 24000},
									"transcription": map[string]any{}, "turn_detection": turnDetection,
								},
								"output": map[string]any{
									"format": map[string]any{"type": "audio/pcm", "rate": 24000}, "voice": "alloy",
								},
							},
						},
					})
					readLiveUntil(t, model, conn, 10*time.Second, func(events []map[string]any) bool {
						return hasLiveEvent(events, "session.updated")
					})

					for _, phrase := range []string{"First turn successful", "Second turn successful"} {
						writeLive(t, model, conn, map[string]any{
							"type": "conversation.item.create", "item": map[string]any{
								"type": "message", "role": "user",
								"content": []map[string]any{{"type": "input_text", "text": "Reply with exactly: " + phrase}},
							},
						})
						writeLive(t, model, conn, map[string]any{"type": "response.create"})
						turn := readGeminiLiveAudioTurn(t, model, conn, false)
						if transcript := outputTranscript(turn); !strings.Contains(strings.ToLower(transcript), strings.ToLower(phrase)) {
							t.Fatalf("text response = %q, want %q", transcript, phrase)
						}
					}

					if len(audio) > 0 {
						if mode == "automatic" {
							// Pace audio and append silence so server VAD can end the turn.
							streamLivePCM(t, model, conn, audio)
						} else {
							appendLivePCM(t, model, conn, audio)
							writeLive(t, model, conn, map[string]any{"type": "input_audio_buffer.commit"})
							writeLive(t, model, conn, map[string]any{"type": "response.create"})
						}
						turn := readGeminiLiveAudioTurn(t, model, conn, true)
						for _, event := range turn {
							if event["type"] == "conversation.item.input_audio_transcription.completed" && event["transcript"] == "" {
								t.Fatal("speech input produced an empty transcript")
							}
						}
					}
					exerciseGeminiLiveTool(t, model, conn)
				})
			}
		})
	}
}

func exerciseGeminiLiveTool(t *testing.T, model string, conn *websocket.Conn) {
	t.Helper()
	writeLive(t, model, conn, map[string]any{
		"type": "conversation.item.create", "item": map[string]any{
			"type": "message", "role": "user",
			"content": []map[string]any{{"type": "input_text", "text": "Use get_weather to check New York's weather, then tell me the temperature."}},
		},
	})
	writeLive(t, model, conn, map[string]any{"type": "response.create"})
	turn := readLiveUntil(t, model, conn, 30*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "response.function_call_arguments.done") && hasLiveEvent(events, "response.done")
	})
	// Intermediate spoken updates must not finish the response before its tool
	// call. A client waiting on response.done must receive the call in that turn.
	validateLiveTrace(t, model, turn)
	assertToolPartialOrder(t, model, turn)
	assertWeatherToolCall(t, model, turn)
	writeLive(t, model, conn, map[string]any{
		"type": "conversation.item.create", "item": map[string]any{
			"type": "function_call_output", "call_id": toolCallID(t, model, turn),
			"output": `{"city":"New York","temperature":22,"unit":"celsius","condition":"sunny"}`,
		},
	})
	writeLive(t, model, conn, map[string]any{"type": "response.create"})
	result := readGeminiLiveAudioTurn(t, model, conn, false)
	transcript := strings.ToLower(outputTranscript(result))
	if !strings.Contains(transcript, "22") && !strings.Contains(transcript, "twenty-two") && !strings.Contains(transcript, "twenty two") {
		t.Fatalf("tool result missing from response: %q", transcript)
	}
}

func readGeminiLiveAudioTurn(t *testing.T, model string, conn *websocket.Conn, transcription bool) []map[string]any {
	t.Helper()
	turn := readLiveUntil(t, model, conn, 30*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "response.done") && (!transcription || hasLiveEvent(events, "conversation.item.input_audio_transcription.completed"))
	})
	validateLiveTrace(t, model, turn)
	assertPartialOrder(t, model, turn, audioResponseOrder())
	for _, eventType := range []string{"response.output_audio.delta", "response.output_audio.done", "response.output_audio_transcript.done"} {
		if !hasLiveEvent(turn, eventType) {
			t.Fatalf("missing %s; events=%v", eventType, liveEventTypes(turn))
		}
	}
	for _, event := range turn {
		if event["type"] == "response.done" && event["response"].(map[string]any)["status"] != "completed" {
			t.Fatalf("response did not complete: %s", compactJSON(event))
		}
	}
	t.Logf("audio response: %q", outputTranscript(turn))
	return turn
}
