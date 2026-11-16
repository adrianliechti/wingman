package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/gorilla/websocket"
)

func TestRealtimeDialerAfterHTTP2Request(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" {
			w.WriteHeader(http.StatusOK)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			conn.Close()
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client := server.Client()
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.ProtoMajor != 2 {
		t.Fatalf("warmup used %s, want HTTP/2", response.Proto)
	}
	conn, _, err := websocketDialer(client).Dial("wss"+strings.TrimPrefix(server.URL, "https")+"/ws", nil)
	if err != nil {
		t.Fatalf("WebSocket after shared HTTP/2 request: %v", err)
	}
	conn.Close()
	// The HTTP client must retain its own HTTP/2 capability.
	response, err = client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.ProtoMajor != 2 {
		t.Fatalf("HTTP client lost HTTP/2: %s", response.Proto)
	}
}

// newOpenAIWireCapture returns a session whose outgoing events are readable
// from the returned channel.
func newOpenAIWireCapture(t *testing.T) (*openAIRealtimeSession, <-chan map[string]any) {
	t.Helper()
	messages := make(chan map[string]any, 16)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(w, request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var message map[string]any
			if err := conn.ReadJSON(&message); err != nil {
				return
			}
			messages <- message
		}
	}))
	t.Cleanup(server.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &openAIRealtimeSession{conn: conn}, messages
}

func readCaptured(t *testing.T, messages <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case message := <-messages:
		return message
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a captured event")
		return nil
	}
}

func TestOpenAIRealtimeConversationItemEditing(t *testing.T) {
	session, captured := newOpenAIWireCapture(t)
	ctx := t.Context()

	message := provider.Message{ID: "ctx_12345", Role: provider.MessageRoleSystem, Content: []provider.Content{provider.TextContent("context")}}
	if err := session.SendMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	created := readCaptured(t, captured)
	item, _ := created["item"].(map[string]any)
	if created["type"] != "conversation.item.create" || item["id"] != "ctx_12345" {
		t.Fatalf("create event = %#v, want the caller's item id", created)
	}

	if err := session.SendMessage(ctx, provider.Message{Role: provider.MessageRoleUser, Content: []provider.Content{provider.TextContent("hi")}}); err != nil {
		t.Fatal(err)
	}
	item, _ = readCaptured(t, captured)["item"].(map[string]any)
	if _, ok := item["id"]; ok {
		t.Fatalf("create event item = %#v, want no id when the caller supplied none", item)
	}

	if err := session.DeleteItem(ctx, "ctx_12345"); err != nil {
		t.Fatal(err)
	}
	deleted := readCaptured(t, captured)
	if deleted["type"] != "conversation.item.delete" || deleted["item_id"] != "ctx_12345" {
		t.Fatalf("delete event = %#v", deleted)
	}
	if err := session.DeleteItem(ctx, ""); err == nil {
		t.Fatal("DeleteItem accepted an empty id")
	}
}

func TestOpenAIRealtimeResponseOverrides(t *testing.T) {
	session, captured := newOpenAIWireCapture(t)

	if err := session.Respond(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if event := readCaptured(t, captured); event["type"] != "response.create" || event["response"] != nil {
		t.Fatalf("plain response.create = %#v, want no response object", event)
	}

	maxTokens := 5
	options := &provider.RealtimeResponseOptions{
		Instructions:     "Summarize.",
		OutputModalities: []provider.RealtimeModality{provider.RealtimeModalityText},
		MaxTokens:        &maxTokens,
		Tools:            []provider.Tool{},
		ToolOptions:      &provider.ToolOptions{Choice: provider.ToolChoiceAny, Allowed: []string{"get_weather"}, DisableParallelToolCalls: true},
		ReasoningOptions: &provider.ReasoningOptions{Effort: provider.EffortLow},
		OutOfBand:        true,
		Input: []provider.Message{
			{ID: "item_1"},
			{Role: provider.MessageRoleUser, Content: []provider.Content{provider.TextContent("hi")}},
			{ID: "item_2", Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "get_weather", Arguments: "{}"})}},
			{Role: provider.MessageRoleUser, Content: []provider.Content{provider.ToolResultContent(provider.ToolResult{ID: "call_1", Parts: []provider.Part{{Text: "sunny"}}})}},
		},
	}
	if err := session.Respond(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	response, _ := readCaptured(t, captured)["response"].(map[string]any)
	want := map[string]any{
		"instructions": "Summarize.", "output_modalities": []any{"text"}, "max_output_tokens": float64(5),
		"tools": []any{}, "tool_choice": map[string]any{"type": "function", "name": "get_weather"},
		"parallel_tool_calls": false, "reasoning": map[string]any{"effort": "low"}, "conversation": "none",
		"input": []any{
			map[string]any{"type": "item_reference", "id": "item_1"},
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
			map[string]any{"type": "function_call", "id": "item_2", "call_id": "call_1", "name": "get_weather", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "sunny"},
		},
	}
	if !reflect.DeepEqual(response, want) {
		t.Fatalf("response overrides = %#v\nwant %#v", response, want)
	}

	if err := session.Respond(t.Context(), &provider.RealtimeResponseOptions{Input: []provider.Message{}, ToolOptions: &provider.ToolOptions{Choice: provider.ToolChoiceAny, Allowed: []string{"a", "b"}}}); err != nil {
		t.Fatal(err)
	}
	response, _ = readCaptured(t, captured)["response"].(map[string]any)
	if want := map[string]any{"input": []any{}, "tool_choice": "required"}; !reflect.DeepEqual(response, want) {
		t.Fatalf("response overrides = %#v, want %#v", response, want)
	}
}

func TestTranslateOutOfBandResponse(t *testing.T) {
	session := &openAIRealtimeSession{}
	for _, test := range []struct {
		conversation string
		want         bool
	}{{`null`, true}, {`"conv_1"`, false}} {
		events := session.translate([]byte(`{"type":"response.created","response":{"id":"resp_1","conversation_id":` + test.conversation + `}}`))
		if len(events) != 1 || events[0].OutOfBand != test.want {
			t.Errorf("conversation_id %s: events = %#v, want OutOfBand %v", test.conversation, events, test.want)
		}
	}
	if events := session.translate([]byte(`{"type":"response.created","response":{"id":"resp_1"}}`)); len(events) != 1 || events[0].OutOfBand {
		t.Errorf("missing conversation_id: events = %#v, want an in-band response", events)
	}
}

func TestValidateOpenAIRealtimeAudioFormats(t *testing.T) {
	defaults := (&Realtime{Config: &Config{}}).Defaults()
	for _, encoding := range []provider.RealtimeAudioEncoding{
		provider.RealtimeAudioPCMU,
		provider.RealtimeAudioPCMA,
	} {
		t.Run(string(encoding), func(t *testing.T) {
			options := defaults
			format := provider.RealtimeAudioFormat{
				Encoding: encoding, SampleRate: 8000, SampleSize: 8, Channels: 1,
			}
			options.Input.Audio = format
			options.Output.Audio = format
			if err := validateOpenAIRealtimeOptions(options, false); err != nil {
				t.Fatalf("valid G.711 format rejected: %v", err)
			}

			options.Input.Audio.SampleSize = 16
			if err := validateOpenAIRealtimeOptions(options, false); err == nil {
				t.Fatal("16-bit G.711 format was accepted")
			}
		})
	}
}

func TestOpenAIRealtimeAudioFormatObject(t *testing.T) {
	pcm := openAIAudioFormat(provider.RealtimeAudioFormat{
		Encoding: provider.RealtimeAudioPCM, SampleRate: 24000, SampleSize: 16, Channels: 1,
	})
	if pcm["type"] != "audio/pcm" || pcm["rate"] != 24000 {
		t.Fatalf("PCM format = %#v", pcm)
	}

	g711 := openAIAudioFormat(provider.RealtimeAudioFormat{
		Encoding: provider.RealtimeAudioPCMU, SampleRate: 8000, SampleSize: 8, Channels: 1,
	})
	if g711["type"] != "audio/pcmu" {
		t.Fatalf("PCMU format = %#v", g711)
	}
	if _, ok := g711["rate"]; ok {
		t.Fatalf("PCMU format includes unsupported rate: %#v", g711)
	}
}

func TestOpenAIRealtimeSessionObjectForcesTracingOff(t *testing.T) {
	object := openAISessionObject((&Realtime{Config: &Config{}}).Defaults())
	tracing, ok := object["tracing"]
	if !ok {
		t.Fatal("session object does not set tracing")
	}
	if tracing != nil {
		t.Fatalf("session tracing = %#v, want null", tracing)
	}
}

func TestOpenAIRealtimeTranscriptionSession(t *testing.T) {
	live, err := NewRealtime("https://api.openai.com/v1", "gpt-live-transcribe", WithToken("test"))
	if err != nil {
		t.Fatal(err)
	}
	if target, _ := live.websocketURL(); target != "wss://api.openai.com/v1/realtime?intent=transcription" {
		t.Fatalf("transcription URL = %q", target)
	}

	defaults := live.Defaults()
	if len(defaults.Output.Modalities) != 1 || defaults.Output.Modalities[0] != provider.RealtimeModalityText {
		t.Fatalf("output modalities = %#v", defaults.Output.Modalities)
	}
	if defaults.Input.Transcription == nil || defaults.Input.Transcription.Model != "gpt-live-transcribe" {
		t.Fatalf("input transcription = %#v", defaults.Input.Transcription)
	}
	// Transcription services own their turn detection until the client chooses.
	if defaults.Input.TurnDetection != nil {
		t.Fatalf("live turn detection = %#v, want the service default", defaults.Input.TurnDetection)
	}

	segmented, err := NewRealtime("https://api.openai.com/v1", "gpt-transcribe", WithToken("test"))
	if err != nil {
		t.Fatal(err)
	}
	if detection := segmented.Defaults().Input.TurnDetection; detection != nil {
		t.Fatalf("gpt-transcribe turn detection = %#v, want the service default", detection)
	}

	conversation, err := NewRealtime("https://api.openai.com/v1", "gpt-realtime-2.1", WithToken("test"))
	if err != nil {
		t.Fatal(err)
	}
	if target, _ := conversation.websocketURL(); target != "wss://api.openai.com/v1/realtime?model=gpt-realtime-2.1" {
		t.Fatalf("conversation URL = %q", target)
	}

	// What the gateway sends for a transcription session: input settings only.
	options := provider.RealtimeOptions{
		Input: provider.RealtimeInputOptions{
			Audio:          defaults.Input.Audio,
			TurnDetection:  &provider.RealtimeTurnDetection{Type: provider.RealtimeTurnDetectionServer, CreateResponse: true},
			Transcription:  &provider.RealtimeTranscription{Model: "alias", Languages: []string{"de", "en"}, Delay: "low"},
			NoiseReduction: provider.RealtimeNoiseReductionNear,
		},
		Output: provider.RealtimeOutputOptions{Modalities: defaults.Output.Modalities},
	}

	session, captured := newOpenAIWireCapture(t)
	session.transcriptionModel = "gpt-live-transcribe"
	if err := session.Update(t.Context(), &options); err != nil {
		t.Fatal(err)
	}
	object, _ := readCaptured(t, captured)["session"].(map[string]any)
	if len(object) != 2 || object["type"] != "transcription" {
		t.Fatalf("session = %#v, want only type and audio", object)
	}
	audio := object["audio"].(map[string]any)
	if len(audio) != 1 {
		t.Fatalf("audio = %#v, want input only", audio)
	}
	input := audio["input"].(map[string]any)
	transcription := input["transcription"].(map[string]any)
	if transcription["model"] != "gpt-live-transcribe" || transcription["delay"] != "low" || len(transcription["languages"].([]any)) != 2 {
		t.Fatalf("transcription = %#v, want the provider model with languages and delay", transcription)
	}
	detection := input["turn_detection"].(map[string]any)
	if _, ok := detection["create_response"]; ok || detection["type"] != "server_vad" {
		t.Fatalf("turn detection = %#v, want server_vad without response control", detection)
	}
	if noise := input["noise_reduction"].(map[string]any); noise["type"] != "near_field" {
		t.Fatalf("noise reduction = %#v", noise)
	}

	// Unset keeps the service's setting; none turns detection off.
	options.Input.TurnDetection = nil
	if err := session.Update(t.Context(), &options); err != nil {
		t.Fatal(err)
	}
	if detection, ok := sessionAudioInput(t, readCaptured(t, captured))["turn_detection"]; ok {
		t.Fatalf("unset turn detection = %#v, want the field omitted", detection)
	}
	options.Input.TurnDetection = &provider.RealtimeTurnDetection{Type: provider.RealtimeTurnDetectionNone}
	if err := session.Update(t.Context(), &options); err != nil {
		t.Fatal(err)
	}
	if detection, ok := sessionAudioInput(t, readCaptured(t, captured))["turn_detection"]; !ok || detection != nil {
		t.Fatalf("disabled turn detection = %#v (present %v), want null", detection, ok)
	}
}

func sessionAudioInput(t *testing.T, event map[string]any) map[string]any {
	t.Helper()
	session, _ := event["session"].(map[string]any)
	audio, _ := session["audio"].(map[string]any)
	input, ok := audio["input"].(map[string]any)
	if !ok {
		t.Fatalf("event = %#v, want session.audio.input", event)
	}
	return input
}

func TestOpenAIRealtimeTranslationSession(t *testing.T) {
	translator, err := NewRealtime("https://api.openai.com/v1", "gpt-realtime-translate", WithToken("test"))
	if err != nil {
		t.Fatal(err)
	}
	if target, _ := translator.websocketURL(); target != "wss://api.openai.com/v1/realtime/translations?model=gpt-realtime-translate" {
		t.Fatalf("translation URL = %q", target)
	}
	defaults := translator.Defaults()
	if defaults.Output.Language != "es" || defaults.Input.TurnDetection != nil {
		t.Fatalf("translation defaults = %#v", defaults)
	}

	// What the gateway sends for a translation session.
	options := provider.RealtimeOptions{
		Input: provider.RealtimeInputOptions{
			Audio:          defaults.Input.Audio,
			Transcription:  &provider.RealtimeTranscription{Model: "gpt-realtime-whisper"},
			NoiseReduction: provider.RealtimeNoiseReductionNear,
		},
		Output: provider.RealtimeOutputOptions{Language: "en"},
	}

	session, captured := newOpenAIWireCapture(t)
	session.translation = true
	if err := session.Update(t.Context(), &options); err != nil {
		t.Fatal(err)
	}
	object, _ := readCaptured(t, captured)["session"].(map[string]any)
	if len(object) != 1 {
		t.Fatalf("session = %#v, want audio only", object)
	}
	audio := object["audio"].(map[string]any)
	if language := audio["output"].(map[string]any)["language"]; language != "en" {
		t.Fatalf("output language = %v", language)
	}
	input := audio["input"].(map[string]any)
	if transcription := input["transcription"].(map[string]any); len(transcription) != 1 || transcription["model"] != "gpt-realtime-whisper" {
		t.Fatalf("transcription = %#v, want the model only", transcription)
	}
	if noise := input["noise_reduction"].(map[string]any); noise["type"] != "near_field" {
		t.Fatalf("noise reduction = %#v", noise)
	}

	if err := session.SendAudio(t.Context(), []byte{0, 0}); err != nil {
		t.Fatal(err)
	}
	if appended := readCaptured(t, captured); appended["type"] != "session.input_audio_buffer.append" {
		t.Fatalf("append event = %#v", appended)
	}
	if err := session.CommitAudio(t.Context()); err != nil {
		t.Fatal(err)
	}
	if closed := readCaptured(t, captured); closed["type"] != "session.close" {
		t.Fatalf("flush event = %#v, want session.close", closed)
	}

	audioDelta := session.translate([]byte(`{"type":"session.output_audio.delta","delta":"AQIDBA==","elapsed_ms":200}`))
	if len(audioDelta) != 1 || audioDelta[0].Type != provider.RealtimeEventAudioDelta || len(audioDelta[0].Audio) != 4 || audioDelta[0].AudioEnd.Milliseconds() != 200 {
		t.Fatalf("audio events = %#v", audioDelta)
	}
	for eventType, role := range map[string]provider.MessageRole{
		"session.output_transcript.delta": provider.MessageRoleAssistant,
		"session.input_transcript.delta":  provider.MessageRoleUser,
	} {
		events := session.translate([]byte(`{"type":"` + eventType + `","delta":" Hi","elapsed_ms":400}`))
		if len(events) != 1 || events[0].Type != provider.RealtimeEventTextDelta || events[0].Role != role || events[0].Text != " Hi" || events[0].AudioEnd.Milliseconds() != 400 {
			t.Fatalf("%s events = %#v", eventType, events)
		}
	}
	if closed := session.translate([]byte(`{"type":"session.closed"}`)); len(closed) != 1 || closed[0].Type != provider.RealtimeEventResponseDone {
		t.Fatalf("closed events = %#v", closed)
	}
}

func TestTranslateInputTranscriptCarriesFinalText(t *testing.T) {
	session := &openAIRealtimeSession{
		contents:    make(map[string]provider.RealtimeContentType),
		transcripts: make(map[string]*strings.Builder),
	}

	session.translate([]byte(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item_1","content_index":0,"delta":" Hello"}`))
	events := session.translate([]byte(`{"type":"conversation.item.input_audio_transcription.completed","item_id":"item_1","content_index":0,"transcript":"Hello"}`))
	if len(events) != 1 || events[0].Type != provider.RealtimeEventContentDone || events[0].Text != "Hello" {
		t.Fatalf("completed events = %#v, want one content done with the final transcript", events)
	}
}

func TestTranslateCurrentInputEvents(t *testing.T) {
	session := &openAIRealtimeSession{
		contents:    make(map[string]provider.RealtimeContentType),
		transcripts: make(map[string]*strings.Builder),
	}

	timeout := session.translate([]byte(`{
		"type":"input_audio_buffer.timeout_triggered",
		"item_id":"item_timeout","audio_start_ms":13216,"audio_end_ms":19232
	}`))
	if len(timeout) != 1 || timeout[0].Type != provider.RealtimeEventInputTimeoutTriggered {
		t.Fatalf("timeout events = %#v", timeout)
	}
	if timeout[0].ItemID != "item_timeout" || timeout[0].AudioStart.Milliseconds() != 13216 || timeout[0].AudioEnd.Milliseconds() != 19232 {
		t.Fatalf("timeout event = %#v", timeout[0])
	}

	segment := session.translate([]byte(`{
		"type":"conversation.item.input_audio_transcription.segment",
		"item_id":"msg_011","content_index":2,"text":"hello","id":"seg_0001",
		"speaker":"spk_1","start":0.1,"end":0.4
	}`))
	if len(segment) != 1 || segment[0].Type != provider.RealtimeEventInputTranscriptionSegment {
		t.Fatalf("segment events = %#v", segment)
	}
	got := segment[0]
	if got.ItemID != "msg_011" || got.ContentIndex != 2 || got.Text != "hello" || got.SegmentID != "seg_0001" || got.Speaker != "spk_1" || got.SegmentStart != 0.1 || got.SegmentEnd != 0.4 {
		t.Fatalf("segment event = %#v", got)
	}
}

func TestTranslateTranscriptionUsage(t *testing.T) {
	duration := 1250 * time.Millisecond
	for _, tc := range []struct {
		name  string
		usage string
		want  provider.RealtimeTranscriptionUsage
	}{
		{"tokens", `,"usage":{"type":"tokens","input_tokens":100,"output_tokens":2,"total_tokens":102,"input_token_details":{"audio_tokens":96,"text_tokens":4}}`, provider.RealtimeTranscriptionUsage{Tokens: &provider.RealtimeUsage{InputTokens: 100, OutputTokens: 2, InputAudioTokens: 96, InputTextTokens: 4}}},
		{"duration", `,"usage":{"type":"duration","seconds":1.25}`, provider.RealtimeTranscriptionUsage{AudioDuration: &duration}},
		{"missing", "", provider.RealtimeTranscriptionUsage{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := &openAIRealtimeSession{transcripts: make(map[string]*strings.Builder)}
			events := session.translate([]byte(`{"type":"conversation.item.input_audio_transcription.completed","item_id":"item_1","content_index":0,"transcript":"Hello"` + tc.usage + `}`))
			if len(events) != 3 {
				t.Fatalf("events = %+v", events)
			}
			done := events[2]
			if done.Type != provider.RealtimeEventContentDone || !reflect.DeepEqual(done.TranscriptionUsage, &tc.want) {
				t.Fatalf("completion = %+v, want transcription usage %+v", done, tc.want)
			}
			if done.Usage != nil {
				t.Fatal("ASR usage must remain separate from generation usage")
			}
		})
	}
}

func TestOpenAIRealtimeFoundryTranscription(t *testing.T) {
	type handshake struct {
		path, query, apiKey, authorization string
	}
	handshakes := make(chan handshake, 1)
	updates := make(chan map[string]any, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handshakes <- handshake{r.URL.Path, r.URL.RawQuery, r.Header.Get("api-key"), r.Header.Get("Authorization")}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if err := conn.WriteJSON(map[string]any{"type": "session.created", "session": map[string]any{"id": "sess_1"}}); err != nil {
			return
		}
		var update map[string]any
		if err := conn.ReadJSON(&update); err != nil {
			return
		}
		updates <- update
		_, _, _ = conn.ReadMessage()
	}))
	t.Cleanup(server.Close)

	realtime, err := NewRealtime("https://example.services.ai.azure.com/mai/v1", "MAI-Transcribe-2-Streaming", WithToken("foundry-key"))
	if err != nil {
		t.Fatal(err)
	}
	if !realtime.RealtimeCapabilities().TranscriptionOnly {
		t.Fatal("MAI-Transcribe-2-Streaming is not a transcription model")
	}
	var target string
	realtime.dial = func(ctx context.Context, url string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		target = url
		local := "ws" + strings.TrimPrefix(server.URL, "http") + strings.TrimPrefix(url, "wss://example.services.ai.azure.com")
		return websocket.DefaultDialer.DialContext(ctx, local, headers)
	}

	// The gateway's options when the client leaves turn detection unset.
	session, err := realtime.Connect(t.Context(), &provider.RealtimeOptions{
		Input:  provider.RealtimeInputOptions{Transcription: &provider.RealtimeTranscription{Model: "mai", Language: "de"}},
		Output: provider.RealtimeOutputOptions{Modalities: []provider.RealtimeModality{provider.RealtimeModalityText}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if target != "wss://example.services.ai.azure.com/mai/v1/realtime?intent=transcription" {
		t.Fatalf("URL = %q", target)
	}
	got := <-handshakes
	if got.path != "/mai/v1/realtime" || got.query != "intent=transcription" {
		t.Fatalf("handshake = %+v", got)
	}
	if got.apiKey != "foundry-key" || got.authorization != "" {
		t.Fatalf("handshake auth = %+v, want the key in api-key", got)
	}

	var update map[string]any
	select {
	case update = <-updates:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for session.update")
	}
	if update["type"] != "session.update" || update["session"].(map[string]any)["type"] != "transcription" {
		t.Fatalf("update = %#v, want a transcription session update", update)
	}
	input := sessionAudioInput(t, update)
	if transcription := input["transcription"].(map[string]any); transcription["model"] != "MAI-Transcribe-2-Streaming" || transcription["language"] != "de" {
		t.Fatalf("transcription = %#v, want the deployment with the language", transcription)
	}
	if format := input["format"].(map[string]any); format["type"] != "audio/pcm" || format["rate"] != float64(24000) {
		t.Fatalf("format = %#v", format)
	}
	if detection, ok := input["turn_detection"]; ok {
		t.Fatalf("turn_detection = %#v, want it left to the service", detection)
	}
}

// The MAI-Transcribe-2-Streaming event sequence from Microsoft's documentation:
// intermediate suffixes, and commit and completion events without item ids.
func TestTranslateMAITranscript(t *testing.T) {
	session := &openAIRealtimeSession{transcripts: make(map[string]*strings.Builder)}
	translate := func(event string) []provider.RealtimeEvent {
		t.Helper()
		return session.translate([]byte(event))
	}

	events := translate(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item_1","content_index":0,"delta":"Hello"}`)
	if len(events) != 2 || events[0].Type != provider.RealtimeEventContentStarted || events[1].Text != "Hello" {
		t.Fatalf("first delta = %#v", events)
	}
	for _, tc := range []struct{ event, want string }{
		{`{"type":"conversation.item.input_audio_transcription.intermediate","item_id":"item_1","content_index":0,"intermediate":" world"}`, "Hello world"},
		{`{"type":"conversation.item.input_audio_transcription.intermediate","item_id":"item_1","content_index":0,"intermediate":" there"}`, "Hello there"},
	} {
		preview := translate(tc.event)
		if len(preview) != 1 || preview[0].Type != provider.RealtimeEventInputTranscriptionPreview || preview[0].Stage != provider.RealtimeGenerationSpeculative {
			t.Fatalf("intermediate = %#v, want a speculative preview", preview)
		}
		if preview[0].Text != tc.want || preview[0].ItemID != "item_1" || preview[0].ContentID != "item_1:0" {
			t.Fatalf("preview = %+v, want %q for item_1", preview[0], tc.want)
		}
	}
	if events := translate(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item_1","content_index":0,"delta":" there!"}`); len(events) != 1 || events[0].Text != " there!" {
		t.Fatalf("second delta = %#v", events)
	}

	committed := translate(`{"type":"input_audio_buffer.committed"}`)
	if len(committed) != 1 || committed[0].Type != provider.RealtimeEventInputCommitted || committed[0].ItemID != "item_1" {
		t.Fatalf("committed = %#v, want item_1", committed)
	}

	// The next item starts before the first one completes.
	translate(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item_2","content_index":0,"delta":"Bye"}`)
	if committed := translate(`{"type":"input_audio_buffer.committed"}`); len(committed) != 1 || committed[0].ItemID != "item_2" {
		t.Fatalf("second commit = %#v, want item_2", committed)
	}

	completed := translate(`{"type":"conversation.item.input_audio_transcription.completed", "transcript":"Hello there!"}`)
	if len(completed) != 1 || completed[0].Type != provider.RealtimeEventContentDone {
		t.Fatalf("completed = %#v, want only content done: the deltas already carried the text", completed)
	}
	if completed[0].ContentID != "item_1:0" || completed[0].ItemID != "item_1" || completed[0].Text != "Hello there!" {
		t.Fatalf("completed = %+v, want item_1", completed[0])
	}
	if completed := translate(`{"type":"conversation.item.input_audio_transcription.completed","transcript":"Bye"}`); len(completed) != 1 || completed[0].ItemID != "item_2" {
		t.Fatalf("second completion = %#v, want item_2", completed)
	}
	if len(session.uncommitted) != 0 || len(session.incomplete) != 0 || len(session.transcripts) != 0 {
		t.Fatalf("leftover state: uncommitted %v, incomplete %v, transcripts %v", session.uncommitted, session.incomplete, session.transcripts)
	}
}
