package realtime_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/server/openai/realtime"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
)

// TestOpenAIRealtimeLiveConformance runs one mixed-input conversation twice:
// first against OpenAI's canonical WebSocket endpoint and then through Wingman's
// OpenAI provider adapter. It compares payload shapes, event families, and
// lifecycle partial orders rather than generated IDs or provider-dependent
// delta chunk boundaries.
//
// This test makes paid API calls and is therefore opt-in:
//
//	WINGMAN_REALTIME_LIVE_E2E=1 go test ./server/openai/realtime \
//	  -run TestOpenAIRealtimeLiveConformance -v
//
// Audio is synthesized through OpenAI by default. Set the two *_AUDIO_FILE
// variables to stable raw PCM16LE/24 kHz/mono or WAV fixtures to avoid TTS calls.
func TestOpenAIRealtimeLiveConformance(t *testing.T) {
	loadDotenv(t)
	if os.Getenv("WINGMAN_REALTIME_LIVE_E2E") != "1" {
		t.Skip("set WINGMAN_REALTIME_LIVE_E2E=1 to run paid OpenAI Realtime conformance tests")
	}

	settings := liveSettings{
		apiKey:          requiredEnv(t, "OPENAI_API_KEY"),
		baseURL:         envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		realtimeModel:   envOr("OPENAI_REALTIME_MODEL", "gpt-realtime-2.1"),
		transcribeModel: envOr("OPENAI_REALTIME_TRANSCRIBE_MODEL", "gpt-live-transcribe"),
		ttsModel:        envOr("OPENAI_REALTIME_TTS_MODEL", "gpt-4o-mini-tts"),
		ttsVoice:        envOr("OPENAI_REALTIME_TTS_VOICE", "alloy"),
	}

	hiAudio := loadOrSynthesizeAudio(t, settings, "OPENAI_REALTIME_HI_AUDIO_FILE", "Hi")
	weatherAudio := loadOrSynthesizeAudio(t, settings, "OPENAI_REALTIME_WEATHER_AUDIO_FILE", "What's the weather in New York?")

	directTarget := liveTarget{
		name: "openai",
		url:  openAIWebSocketURL(t, settings.baseURL, settings.realtimeModel),
		headers: http.Header{
			"Authorization": []string{"Bearer " + settings.apiKey},
		},
	}
	adapterTarget, closeAdapter := startOpenAIAdapter(t, settings)
	defer closeAdapter()

	direct := exerciseLiveSession(t, directTarget, settings, hiAudio, weatherAudio)
	adapted := exerciseLiveSession(t, adapterTarget, settings, hiAudio, weatherAudio)
	defer func() {
		if !t.Failed() {
			return
		}
		for _, value := range []struct {
			name  string
			trace liveTrace
		}{{"openai", direct}, {"wingman-adapter", adapted}} {
			t.Logf("%s voice trace:\n%s", value.name, formatLiveTrace(value.trace.voice))
			t.Logf("%s text trace:\n%s", value.name, formatLiveTrace(value.trace.text))
			t.Logf("%s tool trace:\n%s", value.name, formatLiveTrace(value.trace.tool))
			t.Logf("%s post-tool trace:\n%s", value.name, formatLiveTrace(value.trace.afterTool))
		}
	}()

	compareLiveTurn(t, "voice input + audio/transcript output", direct.voice, adapted.voice, []string{
		"input_audio_buffer.committed",
		"conversation.item.input_audio_transcription.completed",
		"response.created",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_audio.delta",
		"response.output_audio.done",
		"response.output_audio_transcript.done",
		"response.content_part.done",
		"response.output_item.done",
		"response.done",
	})
	assertPartialOrder(t, "direct voice", direct.voice, audioResponseOrder())
	assertPartialOrder(t, "adapter voice", adapted.voice, audioResponseOrder())
	assertTranscriptContains(t, "direct voice", direct.voice, "hi")
	assertTranscriptContains(t, "adapter voice", adapted.voice, "hi")

	compareLiveTurn(t, "text input after voice input", direct.text, adapted.text, []string{
		"response.created",
		"response.output_audio.delta",
		"response.output_audio_transcript.done",
		"response.output_item.done",
		"response.done",
	})
	assertPartialOrder(t, "direct text", direct.text, audioResponseOrder())
	assertPartialOrder(t, "adapter text", adapted.text, audioResponseOrder())

	compareLiveTurn(t, "voice-driven function call", direct.tool, adapted.tool, []string{
		"conversation.item.input_audio_transcription.completed",
		"response.created",
		"response.output_item.added",
		"response.function_call_arguments.done",
		"response.output_item.done",
		"response.done",
	})
	assertToolPartialOrder(t, "direct tool", direct.tool)
	assertToolPartialOrder(t, "adapter tool", adapted.tool)
	assertTranscriptContains(t, "direct weather", direct.tool, "weather", "new york")
	assertTranscriptContains(t, "adapter weather", adapted.tool, "weather", "new york")
	assertWeatherToolCall(t, "direct", direct.tool)
	assertWeatherToolCall(t, "adapter", adapted.tool)

	compareLiveTurn(t, "post-tool multi-output lifecycle", direct.afterTool, adapted.afterTool, []string{
		"response.created",
		"response.output_audio.delta",
		"response.output_audio_transcript.done",
		"response.output_item.done",
		"response.done",
	})
	assertPartialOrder(t, "direct post-tool", direct.afterTool, audioResponseOrder())
	assertPartialOrder(t, "adapter post-tool", adapted.afterTool, audioResponseOrder())
}

type liveSettings struct {
	apiKey          string
	baseURL         string
	realtimeModel   string
	transcribeModel string
	ttsModel        string
	ttsVoice        string
}

type liveTarget struct {
	name    string
	url     string
	headers http.Header
}

type liveTrace struct {
	opening   []map[string]any
	voice     []map[string]any
	text      []map[string]any
	tool      []map[string]any
	afterTool []map[string]any
}

func exerciseLiveSession(t *testing.T, target liveTarget, settings liveSettings, hiAudio, weatherAudio []byte) liveTrace {
	t.Helper()
	conn, response, err := websocket.DefaultDialer.Dial(target.url, target.headers)
	if err != nil {
		if response != nil {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
			t.Fatalf("%s websocket handshake: %v (%s: %s)", target.name, err, response.Status, body)
		}
		t.Fatalf("%s websocket handshake: %v", target.name, err)
	}
	defer conn.Close()

	trace := liveTrace{}
	defer func() {
		if t.Failed() {
			t.Logf("%s opening trace:\n%s", target.name, formatLiveTrace(trace.opening))
			t.Logf("%s voice trace:\n%s", target.name, formatLiveTrace(trace.voice))
			t.Logf("%s text trace:\n%s", target.name, formatLiveTrace(trace.text))
			t.Logf("%s tool trace:\n%s", target.name, formatLiveTrace(trace.tool))
			t.Logf("%s post-tool trace:\n%s", target.name, formatLiveTrace(trace.afterTool))
		}
	}()

	trace.opening = readLiveUntil(t, target.name, conn, 20*time.Second, func(events []map[string]any) bool {
		// conversation.created is part of the GA event schema, but the native
		// WebSocket endpoint does not consistently emit it for every model.
		return hasLiveEvent(events, "session.created")
	})

	writeLive(t, target.name, conn, map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"type":              "realtime",
			"instructions":      "Answer in one short sentence. Only use get_weather for weather questions.",
			"output_modalities": []string{"audio"},
			"tool_choice":       "none",
			"tools":             []map[string]any{weatherTool()},
			"audio": map[string]any{
				"input": map[string]any{
					"format":         map[string]any{"type": "audio/pcm", "rate": 24000},
					"transcription":  map[string]any{"model": settings.transcribeModel},
					"turn_detection": nil,
				},
				"output": map[string]any{
					"format": map[string]any{"type": "audio/pcm", "rate": 24000},
					"voice":  "alloy",
				},
			},
		},
	})
	trace.opening = append(trace.opening, readLiveUntil(t, target.name, conn, 20*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "session.updated")
	})...)

	appendLivePCM(t, target.name, conn, hiAudio)
	writeLive(t, target.name, conn, map[string]any{"type": "input_audio_buffer.commit"})
	writeLive(t, target.name, conn, map[string]any{"type": "response.create"})
	trace.voice = readLiveUntil(t, target.name, conn, 60*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "conversation.item.input_audio_transcription.completed") && hasLiveEvent(events, "response.done")
	})

	writeLive(t, target.name, conn, map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type": "message", "role": "user",
			"content": []map[string]any{{"type": "input_text", "text": "Reply with exactly: text turn received"}},
		},
	})
	writeLive(t, target.name, conn, map[string]any{"type": "response.create"})
	trace.text = readLiveUntil(t, target.name, conn, 60*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "response.done")
	})

	writeLive(t, target.name, conn, map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"type": "realtime", "tool_choice": "required",
			"instructions": "For the next weather question, call get_weather. Do not answer from memory.",
		},
	})
	_ = readLiveUntil(t, target.name, conn, 20*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "session.updated")
	})

	appendLivePCM(t, target.name, conn, weatherAudio)
	writeLive(t, target.name, conn, map[string]any{"type": "input_audio_buffer.commit"})
	writeLive(t, target.name, conn, map[string]any{"type": "response.create"})
	trace.tool = readLiveUntil(t, target.name, conn, 60*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "conversation.item.input_audio_transcription.completed") &&
			hasLiveEvent(events, "response.function_call_arguments.done") &&
			hasLiveEvent(events, "response.done")
	})
	callID := toolCallID(t, target.name, trace.tool)

	writeLive(t, target.name, conn, map[string]any{
		"type": "session.update", "session": map[string]any{"type": "realtime", "tool_choice": "none"},
	})
	_ = readLiveUntil(t, target.name, conn, 20*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "session.updated")
	})
	writeLive(t, target.name, conn, map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type": "function_call_output", "call_id": callID,
			"output": `{"city":"New York","temperature_c":22,"condition":"sunny"}`,
		},
	})
	writeLive(t, target.name, conn, map[string]any{"type": "response.create"})
	trace.afterTool = readLiveUntil(t, target.name, conn, 60*time.Second, func(events []map[string]any) bool {
		return hasLiveEvent(events, "response.done")
	})

	validateLiveTrace(t, target.name+" opening", trace.opening)
	validateLiveTrace(t, target.name+" voice", trace.voice)
	validateLiveTrace(t, target.name+" text", trace.text)
	validateLiveTrace(t, target.name+" tool", trace.tool)
	validateLiveTrace(t, target.name+" post-tool", trace.afterTool)
	return trace
}

func startOpenAIAdapter(t *testing.T, settings liveSettings) (liveTarget, func()) {
	t.Helper()
	upstream, err := openai.NewRealtime(settings.baseURL, settings.realtimeModel, openai.WithToken(settings.apiKey))
	if err != nil {
		t.Fatalf("create OpenAI realtime provider: %v", err)
	}
	cfg := &config.Config{}
	cfg.RegisterRealtime(settings.realtimeModel, upstream)
	router := chi.NewRouter()
	realtime.New(cfg).Attach(router)
	server := httptest.NewServer(router)
	target := "ws" + strings.TrimPrefix(server.URL, "http") + "/realtime?model=" + url.QueryEscape(settings.realtimeModel)
	return liveTarget{name: "wingman-adapter", url: target}, server.Close
}

func loadOrSynthesizeAudio(t *testing.T, settings liveSettings, pathEnv, phrase string) []byte {
	t.Helper()
	if path := os.Getenv(pathEnv); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", pathEnv, err)
		}
		return normalizePCMFixture(t, path, data)
	}

	body, err := json.Marshal(map[string]any{
		"model": settings.ttsModel, "voice": settings.ttsVoice,
		"input": phrase, "response_format": "pcm",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(settings.baseURL, "/")+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+settings.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("synthesize %q fixture: %v", phrase, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		t.Fatalf("read %q fixture: %v", phrase, err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("synthesize %q fixture: %s: %s", phrase, response.Status, data)
	}
	return normalizePCMFixture(t, "OpenAI TTS", data)
}

func normalizePCMFixture(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
		var format []byte
		var audio []byte
		for offset := 12; offset+8 <= len(data); {
			kind := string(data[offset : offset+4])
			size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
			start := offset + 8
			end := start + size
			if end > len(data) {
				t.Fatalf("%s has a truncated WAV %s chunk", name, kind)
			}
			switch kind {
			case "fmt ":
				format = data[start:end]
			case "data":
				audio = data[start:end]
			}
			offset = end + size%2
		}
		if len(format) < 16 || binary.LittleEndian.Uint16(format[0:2]) != 1 ||
			binary.LittleEndian.Uint16(format[2:4]) != 1 ||
			binary.LittleEndian.Uint32(format[4:8]) != 24000 ||
			binary.LittleEndian.Uint16(format[14:16]) != 16 {
			t.Fatalf("%s must be PCM16LE, 24 kHz, mono", name)
		}
		data = audio
	}
	if len(data) < 4800 || len(data)%2 != 0 {
		t.Fatalf("%s contains %d PCM bytes; want at least 100 ms of aligned PCM16", name, len(data))
	}
	return data
}

func openAIWebSocketURL(t *testing.T, baseURL, model string) string {
	t.Helper()
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "wss", "ws":
	default:
		t.Fatalf("unsupported OpenAI base URL scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/realtime"
	query := u.Query()
	query.Set("model", model)
	u.RawQuery = query.Encode()
	return u.String()
}

func appendLivePCM(t *testing.T, name string, conn *websocket.Conn, audio []byte) {
	t.Helper()
	const chunkBytes = 4800 // 100 ms at PCM16LE/24 kHz/mono.
	for chunk := range slices.Chunk(audio, chunkBytes) {
		writeLive(t, name, conn, map[string]any{
			"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(chunk),
		})
	}
}

func writeLive(t *testing.T, name string, conn *websocket.Conn, event any) {
	t.Helper()
	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(event); err != nil {
		t.Fatalf("%s write event: %v", name, err)
	}
}

func readLiveUntil(t *testing.T, name string, conn *websocket.Conn, timeout time.Duration, done func([]map[string]any) bool) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var events []map[string]any
	for len(events) < 1000 {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		var event map[string]any
		if err := conn.ReadJSON(&event); err != nil {
			t.Fatalf("%s read event: %v\ntrace:\n%s", name, err, formatLiveTrace(events))
		}
		events = append(events, event)
		if event["type"] == "error" {
			t.Fatalf("%s returned an error event: %s\ntrace:\n%s", name, compactJSON(event), formatLiveTrace(events))
		}
		if done(events) {
			return events
		}
	}
	t.Fatalf("%s emitted too many events without reaching the terminal condition\n%s", name, formatLiveTrace(events))
	return nil
}

func compareLiveTurn(t *testing.T, label string, direct, adapted []map[string]any, required []string) {
	t.Helper()
	for _, eventType := range required {
		if !hasLiveEvent(direct, eventType) {
			t.Errorf("%s: OpenAI trace is missing %s\n%s", label, eventType, formatLiveTrace(direct))
		}
		if !hasLiveEvent(adapted, eventType) {
			t.Errorf("%s: adapter trace is missing OpenAI event %s\n%s", label, eventType, formatLiveTrace(adapted))
		}
	}

	// Compare the event families the WebSocket client observes. Repeated delta
	// chunks are collapsed, and asynchronous transcription events are compared
	// for presence rather than absolute placement relative to generation.
	for _, eventType := range comparableEventTypes() {
		if hasLiveEvent(direct, eventType) && !hasLiveEvent(adapted, eventType) {
			t.Errorf("%s: adapter lost direct event family %s", label, eventType)
		}
	}
}

func validateLiveTrace(t *testing.T, label string, events []map[string]any) {
	t.Helper()
	for _, event := range events {
		eventType, ok := event["type"].(string)
		if !ok || eventType == "" {
			t.Errorf("%s: event lacks type: %s", label, compactJSON(event))
			continue
		}
		if id, ok := event["event_id"].(string); !ok || id == "" {
			t.Errorf("%s: %s lacks event_id: %s", label, eventType, compactJSON(event))
		}
		switch eventType {
		case "session.created", "session.updated":
			assertLiveObjectWithString(t, label, event, "session", "id")
		case "conversation.created":
			assertLiveObjectWithString(t, label, event, "conversation", "id")
		case "conversation.item.input_audio_transcription.completed":
			assertLiveString(t, label, event, "item_id")
			if _, ok := event["transcript"].(string); !ok {
				t.Errorf("%s: transcription transcript is not a string: %s", label, compactJSON(event))
			}
		case "response.created", "response.done":
			assertLiveObjectWithString(t, label, event, "response", "id")
		case "response.output_audio.delta":
			assertLiveString(t, label, event, "response_id")
			assertLiveString(t, label, event, "item_id")
			delta, _ := event["delta"].(string)
			if decoded, err := base64.StdEncoding.DecodeString(delta); err != nil || len(decoded) == 0 {
				t.Errorf("%s: invalid audio delta: %v", label, err)
			}
		case "response.function_call_arguments.delta":
			assertLiveString(t, label, event, "item_id")
			if _, ok := event["delta"].(string); !ok {
				t.Errorf("%s: function delta is not a string", label)
			}
		case "response.function_call_arguments.done":
			assertLiveString(t, label, event, "item_id")
			assertLiveString(t, label, event, "call_id")
			assertLiveString(t, label, event, "name")
			if arguments, ok := event["arguments"].(string); !ok || !json.Valid([]byte(arguments)) {
				t.Errorf("%s: function arguments are not valid JSON: %v", label, event["arguments"])
			}
		case "response.output_item.done":
			assertLiveObjectWithString(t, label, event, "item", "id")
		}
	}
}

func assertPartialOrder(t *testing.T, label string, events []map[string]any, edges [][2]string) {
	t.Helper()
	for _, edge := range edges {
		before := firstLiveEvent(events, edge[0])
		after := firstLiveEvent(events, edge[1])
		if before < 0 || after < 0 {
			continue
		}
		if before >= after {
			t.Errorf("%s: %s must precede %s; types=%v", label, edge[0], edge[1], liveEventTypes(events))
		}
	}
}

func audioResponseOrder() [][2]string {
	return [][2]string{
		{"response.created", "response.output_item.added"},
		{"response.output_item.added", "response.content_part.added"},
		{"response.content_part.added", "response.output_audio.delta"},
		{"response.output_audio.delta", "response.output_audio.done"},
		{"response.output_audio.done", "response.content_part.done"},
		{"response.content_part.done", "response.output_item.done"},
		{"response.output_item.done", "response.done"},
	}
}

func assertToolPartialOrder(t *testing.T, label string, events []map[string]any) {
	t.Helper()
	created := firstLiveEvent(events, "response.created")
	argumentsDone := firstLiveEvent(events, "response.function_call_arguments.done")
	responseDone := firstLiveEvent(events, "response.done")
	toolItemDone := -1
	for index, event := range events {
		if event["type"] != "response.output_item.done" {
			continue
		}
		item, _ := event["item"].(map[string]any)
		if item["type"] == "function_call" {
			toolItemDone = index
			break
		}
	}
	if created < 0 || argumentsDone < 0 || toolItemDone < 0 || responseDone < 0 {
		t.Errorf("%s: incomplete tool lifecycle: %v", label, liveEventTypes(events))
		return
	}
	if !(created < argumentsDone && argumentsDone < toolItemDone && toolItemDone < responseDone) {
		t.Errorf("%s: want response.created < function arguments done < function item done < response.done; types=%v", label, liveEventTypes(events))
	}
}

func assertTranscriptContains(t *testing.T, label string, events []map[string]any, alternatives ...string) {
	t.Helper()
	for _, event := range events {
		if event["type"] != "conversation.item.input_audio_transcription.completed" {
			continue
		}
		transcript := strings.ToLower(event["transcript"].(string))
		for _, alternative := range alternatives {
			if strings.Contains(transcript, strings.ToLower(alternative)) {
				return
			}
		}
		t.Errorf("%s transcript %q contains none of %v", label, transcript, alternatives)
		return
	}
	t.Errorf("%s has no completed transcription", label)
}

func assertWeatherToolCall(t *testing.T, label string, events []map[string]any) {
	t.Helper()
	for _, event := range events {
		if event["type"] != "response.function_call_arguments.done" {
			continue
		}
		if event["name"] != "get_weather" {
			t.Errorf("%s tool name = %v, want get_weather", label, event["name"])
		}
		arguments, _ := event["arguments"].(string)
		if !strings.Contains(strings.ToLower(arguments), "new york") {
			t.Errorf("%s tool arguments = %s, want New York", label, arguments)
		}
		return
	}
	t.Errorf("%s did not emit a completed function call", label)
}

func weatherTool() map[string]any {
	return map[string]any{
		"type": "function", "name": "get_weather", "description": "Get current weather for a city.",
		"parameters": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"city": map[string]any{"type": "string"}},
			"required":   []string{"city"},
		},
	}
}

func toolCallID(t *testing.T, label string, events []map[string]any) string {
	t.Helper()
	for _, event := range events {
		if event["type"] == "response.function_call_arguments.done" {
			if id, ok := event["call_id"].(string); ok && id != "" {
				return id
			}
		}
	}
	t.Fatalf("%s trace contains no tool call ID\n%s", label, formatLiveTrace(events))
	return ""
}

func comparableEventTypes() []string {
	return []string{
		"input_audio_buffer.committed",
		"conversation.item.input_audio_transcription.delta",
		"conversation.item.input_audio_transcription.completed",
		"response.created",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_audio_transcript.delta",
		"response.output_audio.delta",
		"response.output_audio.done",
		"response.output_audio_transcript.done",
		"response.content_part.done",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.done",
		"response.done",
	}
}

func hasLiveEvent(events []map[string]any, eventType string) bool {
	return firstLiveEvent(events, eventType) >= 0
}

func firstLiveEvent(events []map[string]any, eventType string) int {
	for index, event := range events {
		if event["type"] == eventType {
			return index
		}
	}
	return -1
}

func liveEventTypes(events []map[string]any) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		if eventType, ok := event["type"].(string); ok {
			types = append(types, eventType)
		}
	}
	return types
}

func assertLiveString(t *testing.T, label string, event map[string]any, key string) {
	t.Helper()
	if value, ok := event[key].(string); !ok || value == "" {
		t.Errorf("%s: %s.%s is not a non-empty string: %s", label, event["type"], key, compactJSON(event))
	}
}

func assertLiveObjectWithString(t *testing.T, label string, event map[string]any, objectKey, key string) {
	t.Helper()
	object, ok := event[objectKey].(map[string]any)
	if !ok {
		t.Errorf("%s: %s.%s is not an object: %s", label, event["type"], objectKey, compactJSON(event))
		return
	}
	assertLiveString(t, label, object, key)
}

func formatLiveTrace(events []map[string]any) string {
	data, _ := json.MarshalIndent(events, "", "  ")
	return string(data)
}

func compactJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func loadDotenv(t *testing.T) {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		path := filepath.Join(directory, ".env")
		if _, err := os.Stat(path); err == nil {
			_ = godotenv.Load(path)
			return
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return
		}
		directory = parent
	}
}

func requiredEnv(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("%s is required when WINGMAN_REALTIME_LIVE_E2E=1", name)
	}
	return value
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
