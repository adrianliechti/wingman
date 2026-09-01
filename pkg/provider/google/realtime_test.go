package google

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/gorilla/websocket"
)

func TestGeminiSetupUsesJSONSchemaToolParameters(t *testing.T) {
	realtime, err := NewRealtime("", "gemini-3.1-flash-live-preview", WithToken("test"))
	if err != nil {
		t.Fatal(err)
	}
	options := realtime.Defaults()
	options.Tools = []provider.Tool{{
		Name: "complex_tool", Description: "Exercises the production tool schema surface.",
		Parameters: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"value": map[string]any{
					"anyOf": []any{
						map[string]any{"type": "string"},
						map[string]any{"type": "null"},
					},
				},
			},
			"required": []string{"value"},
		},
	}}

	message := geminiSetup("gemini-3.1-flash-live-preview", options)
	setup := message["setup"].(map[string]any)
	tools := setup["tools"].([]map[string]any)
	declarations := tools[0]["functionDeclarations"].([]map[string]any)
	declaration := declarations[0]
	if _, exists := declaration["parameters"]; exists {
		t.Fatal("Gemini setup used restricted parameters instead of parametersJsonSchema")
	}
	schema, ok := declaration["parametersJsonSchema"].(map[string]any)
	if !ok {
		t.Fatalf("parametersJsonSchema = %#v", declaration["parametersJsonSchema"])
	}
	if schema["additionalProperties"] != false {
		t.Errorf("JSON Schema keywords were not preserved: %#v", schema)
	}
}

func TestGeminiOpenAIVoiceAliases(t *testing.T) {
	tests := map[string]string{
		"alloy": "Kore",
		"marin": "Kore",
		"onyx":  "Puck",
		"Kore":  "Kore",
	}
	for input, want := range tests {
		if got := voiceForGemini(input); got != want {
			t.Errorf("voiceForGemini(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGeminiSetupMapsTranscriptionAndVAD(t *testing.T) {
	realtime, err := NewRealtime("", "gemini-3.1-flash-live-preview", WithToken("test"))
	if err != nil {
		t.Fatal(err)
	}
	options := realtime.Defaults()
	options.InputTranscription = &provider.RealtimeTranscription{Language: "de-CH"}
	options.TurnDetection = &provider.RealtimeTurnDetection{
		Type: provider.RealtimeTurnDetectionSemantic, Eagerness: "high",
		PrefixPadding: 120 * time.Millisecond, SilenceDuration: 800 * time.Millisecond,
		CreateResponse: true, InterruptResponse: false,
	}

	setup := geminiSetup("gemini-3.1-flash-live-preview", options)["setup"].(map[string]any)
	transcription := setup["inputAudioTranscription"].(map[string]any)
	languages := transcription["languageCodes"].([]string)
	if len(languages) != 1 || languages[0] != "de-CH" {
		t.Fatalf("input transcription languages = %#v, want de-CH", languages)
	}
	input := setup["realtimeInputConfig"].(map[string]any)
	if got := input["activityHandling"]; got != "NO_INTERRUPTION" {
		t.Errorf("activityHandling = %v, want NO_INTERRUPTION", got)
	}
	detection := input["automaticActivityDetection"].(map[string]any)
	if detection["disabled"] != false || detection["prefixPaddingMs"] != int64(120) || detection["silenceDurationMs"] != int64(800) {
		t.Errorf("automaticActivityDetection = %#v", detection)
	}
	if got := detection["endOfSpeechSensitivity"]; got != "END_SENSITIVITY_HIGH" {
		t.Errorf("endOfSpeechSensitivity = %v", got)
	}

	options.TurnDetection = nil
	manual := geminiSetup("gemini-3.1-flash-live-preview", options)["setup"].(map[string]any)
	manualDetection := manual["realtimeInputConfig"].(map[string]any)["automaticActivityDetection"].(map[string]any)
	if manualDetection["disabled"] != true {
		t.Errorf("manual VAD setup = %#v", manualDetection)
	}
}

func TestGeminiRejectsTurnDetectionWithoutAutomaticResponse(t *testing.T) {
	realtime, err := NewRealtime("", "gemini-3.1-flash-live-preview", WithToken("test"))
	if err != nil {
		t.Fatal(err)
	}
	options := realtime.Defaults()
	options.TurnDetection.CreateResponse = false
	if err := validateGeminiRealtimeOptions(options); err == nil || !strings.Contains(err.Error(), "response creation") {
		t.Fatalf("validate error = %v, want unsupported automatic response error", err)
	}
}

func TestGeminiLiveTranscriptionDefaultsAndValidation(t *testing.T) {
	realtime, err := NewRealtime("", "gemini-3.5-transcribe-live", WithToken("test"))
	if err != nil {
		t.Fatal(err)
	}
	defaults := realtime.Defaults()
	if len(defaults.OutputModalities) != 1 || defaults.OutputModalities[0] != provider.RealtimeModalityText {
		t.Fatalf("transcription output modalities = %#v", defaults.OutputModalities)
	}
	if defaults.InputTranscription == nil || defaults.OutputTranscription {
		t.Fatalf("transcription defaults = %#v", defaults)
	}
	setup := geminiSetup("gemini-3.5-transcribe-live", defaults)["setup"].(map[string]any)
	if _, ok := setup["inputAudioTranscription"]; !ok {
		t.Fatal("transcription setup did not enable inputAudioTranscription")
	}
	generation := setup["generationConfig"].(map[string]any)
	modalities := generation["responseModalities"].([]string)
	if len(modalities) != 1 || modalities[0] != "TEXT" {
		t.Errorf("transcription setup modalities = %#v", modalities)
	}

	invalid := defaults
	invalid.OutputModalities = []provider.RealtimeModality{provider.RealtimeModalityAudio}
	if err := validateGeminiRealtimeModelOptions("gemini-3.5-transcribe-live", invalid); err == nil {
		t.Fatal("dedicated transcription model accepted audio response modality")
	}
}

func TestGeminiAffectiveDialogIsOptInAndModelGated(t *testing.T) {
	if _, err := NewRealtime("", "gemini-3.1-flash-live-preview", WithToken("test"), WithAffectiveDialog(true)); err == nil {
		t.Fatal("Gemini 3.1 accepted unsupported affective dialog")
	}

	realtime, err := NewRealtime("", "gemini-2.5-flash-native-audio-preview-12-2025", WithToken("test"), WithAffectiveDialog(true))
	if err != nil {
		t.Fatal(err)
	}
	setup := realtime.setup(realtime.Defaults())["setup"].(map[string]any)
	if setup["enableAffectiveDialog"] != true {
		t.Errorf("enableAffectiveDialog = %v, want true", setup["enableAffectiveDialog"])
	}
	if !realtime.Capabilities().Interruption {
		t.Error("Gemini 2.5 should support clientContent interruption")
	}

	threeOne, err := NewRealtime("", "gemini-3.1-flash-live-preview", WithToken("test"))
	if err != nil {
		t.Fatal(err)
	}
	threeOneSetup := threeOne.setup(threeOne.Defaults())["setup"].(map[string]any)
	if _, exists := threeOneSetup["enableAffectiveDialog"]; exists {
		t.Fatal("Gemini 3.1 setup unexpectedly enabled affective dialog")
	}
	capabilities := threeOne.Capabilities()
	if capabilities.Interruption || !capabilities.InputActivityEvents {
		t.Errorf("Gemini 3.1 capabilities = %#v", capabilities)
	}
}

func TestGeminiTranscriptionActivityFinalizesBeforeModelTurn(t *testing.T) {
	session := newGeminiTranslationTestSession()

	started := session.translate(decodeGeminiMessage(t, `{"voiceActivity":{"voiceActivityType":"ACTIVITY_START"}}`))
	assertGeminiEventTypes(t, started, provider.RealtimeEventInputSpeechStarted)
	itemID := started[0].ItemID
	if itemID == "" {
		t.Fatal("activity start has no item ID")
	}

	preview := session.translate(decodeGeminiMessage(t, `{"serverContent":{"interimInputTranscription":{"text":"Hal"}}}`))
	assertGeminiEventTypes(t, preview, provider.RealtimeEventInputTranscriptionPreview)
	if preview[0].Text != "Hal" || preview[0].Stage != provider.RealtimeGenerationSpeculative {
		t.Errorf("preview = %#v", preview[0])
	}

	finalWhileSpeaking := session.translate(decodeGeminiMessage(t, `{"serverContent":{"inputTranscription":{"text":"Hallo."}}}`))
	if len(finalWhileSpeaking) != 0 {
		t.Fatalf("final transcript emitted before activity end: %#v", finalWhileSpeaking)
	}

	ended := session.translate(decodeGeminiMessage(t, `{"voiceActivity":{"voiceActivityType":"ACTIVITY_END"}}`))
	assertGeminiEventTypes(t, ended,
		provider.RealtimeEventInputSpeechStopped,
		provider.RealtimeEventInputCommitted,
		provider.RealtimeEventContentStarted,
		provider.RealtimeEventTextDelta,
		provider.RealtimeEventContentDone,
	)
	for _, event := range ended {
		if event.ItemID != itemID {
			t.Errorf("%s item ID = %q, want %q", event.Type, event.ItemID, itemID)
		}
	}
	if ended[3].Text != "Hallo." {
		t.Errorf("final transcript = %q", ended[3].Text)
	}

	// inputTranscription is authoritative and independently ordered. Without a
	// voiceActivity envelope it must complete immediately, not wait for the
	// assistant's turnComplete event.
	standalone := newGeminiTranslationTestSession().translate(decodeGeminiMessage(t, `{"serverContent":{"inputTranscription":{"text":"Hello."}}}`))
	assertGeminiEventTypes(t, standalone,
		provider.RealtimeEventContentStarted,
		provider.RealtimeEventTextDelta,
		provider.RealtimeEventContentDone,
	)
}

func TestGeminiInterruptionWithoutPriorOutputStillCompletesResponse(t *testing.T) {
	session := newGeminiTranslationTestSession()
	interrupted := session.translate(decodeGeminiMessage(t, `{"serverContent":{"interrupted":true}}`))
	assertGeminiEventTypes(t, interrupted,
		provider.RealtimeEventResponseStarted,
		provider.RealtimeEventInterrupted,
	)
	if interrupted[1].ResponseID == "" {
		t.Fatal("interruption has no response ID")
	}

	done := session.translate(decodeGeminiMessage(t, `{"serverContent":{"turnComplete":true}}`))
	assertGeminiEventTypes(t, done, provider.RealtimeEventResponseDone)
	if done[0].StopReason != "interrupted" {
		t.Errorf("stop reason = %q", done[0].StopReason)
	}
}

func TestGeminiToolCancellationRejectsLateResults(t *testing.T) {
	session := newGeminiTranslationTestSession()
	called := session.translate(decodeGeminiMessage(t, `{"toolCall":{"functionCalls":[{"id":"call-1","name":"get_weather"}]}}`))
	assertGeminiEventTypes(t, called,
		provider.RealtimeEventResponseStarted,
		provider.RealtimeEventContentStarted,
		provider.RealtimeEventToolCall,
		provider.RealtimeEventContentDone,
		provider.RealtimeEventResponseDone,
	)
	if got := called[2].ToolCall.Arguments; got != "{}" {
		t.Errorf("nil tool arguments = %q, want {}", got)
	}

	cancelled := session.translate(decodeGeminiMessage(t, `{"toolCallCancellation":{"ids":["call-1"]}}`))
	if len(cancelled) != 0 {
		t.Fatalf("tool cancellation manufactured events: %#v", cancelled)
	}
	if err := session.SendToolResult(context.Background(), "call-1", `{}`); err == nil || !strings.Contains(err.Error(), "unknown tool call") {
		t.Fatalf("late tool result error = %v", err)
	}
}

func TestGeminiManualActivityFramesAudioAndText(t *testing.T) {
	session, messages, closeCapture := newGeminiWireCapture(t)
	defer closeCapture()

	if err := session.SendAudio(context.Background(), []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := session.CommitAudio(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGeminiRealtimeInputKeys(t, messages, "activityStart", "audio", "activityEnd")

	if err := session.SendMessage(context.Background(), provider.UserMessage("hello")); err != nil {
		t.Fatal(err)
	}
	assertGeminiRealtimeInputKeys(t, messages, "activityStart", "text", "activityEnd")
}

func newGeminiTranslationTestSession() *geminiRealtimeSession {
	return &geminiRealtimeSession{toolNames: make(map[string]string)}
}

func decodeGeminiMessage(t *testing.T, raw string) geminiServerMessage {
	t.Helper()
	var message geminiServerMessage
	if err := json.Unmarshal([]byte(raw), &message); err != nil {
		t.Fatal(err)
	}
	return message
}

func assertGeminiEventTypes(t *testing.T, events []provider.RealtimeEvent, want ...provider.RealtimeEventType) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d: %#v", len(events), len(want), events)
	}
	for i, eventType := range want {
		if events[i].Type != eventType {
			t.Fatalf("event[%d] = %q, want %q: %#v", i, events[i].Type, eventType, events)
		}
	}
}

func newGeminiWireCapture(t *testing.T) (*geminiRealtimeSession, <-chan map[string]any, func()) {
	t.Helper()
	messages := make(chan map[string]any, 16)
	readErrors := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(w, request, nil)
		if err != nil {
			readErrors <- err
			return
		}
		defer conn.Close()
		for {
			var message map[string]any
			if err := conn.ReadJSON(&message); err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					readErrors <- err
				}
				return
			}
			messages <- message
		}
	}))

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	session := &geminiRealtimeSession{
		ctx: ctx, cancel: cancel, conn: conn, events: make(chan provider.RealtimeEvent, 16),
		options: provider.RealtimeOptions{
			InputAudio: provider.RealtimeAudioFormat{Encoding: provider.RealtimeAudioPCM, SampleRate: 16000, SampleSize: 16, Channels: 1},
		},
		toolNames: make(map[string]string),
	}
	cleanup := func() {
		_ = session.Close()
		server.Close()
		select {
		case err := <-readErrors:
			if err != nil && !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "use of closed network connection") {
				t.Errorf("capture websocket: %v", err)
			}
		default:
		}
	}
	return session, messages, cleanup
}

func assertGeminiRealtimeInputKeys(t *testing.T, messages <-chan map[string]any, want ...string) {
	t.Helper()
	for i, key := range want {
		select {
		case message := <-messages:
			realtimeInput, ok := message["realtimeInput"].(map[string]any)
			if !ok {
				t.Fatalf("message[%d] has no realtimeInput: %#v", i, message)
			}
			if _, ok := realtimeInput[key]; !ok {
				t.Fatalf("message[%d] = %#v, want key %q", i, realtimeInput, key)
			}
			if key == "audio" {
				audio := realtimeInput[key].(map[string]any)
				if audio["data"] != base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4}) || audio["mimeType"] != "audio/pcm;rate=16000" {
					t.Errorf("audio message = %#v", audio)
				}
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for Gemini message[%d] key %q", i, key)
		}
	}
}
