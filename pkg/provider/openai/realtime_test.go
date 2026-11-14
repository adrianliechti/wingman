package openai

import (
	"net/http"
	"net/http/httptest"
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
			options.InputAudio = format
			options.OutputAudio = format
			if err := validateOpenAIRealtimeOptions(options, false); err != nil {
				t.Fatalf("valid G.711 format rejected: %v", err)
			}

			options.InputAudio.SampleSize = 16
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
	if len(defaults.OutputModalities) != 1 || defaults.OutputModalities[0] != provider.RealtimeModalityText {
		t.Fatalf("output modalities = %#v", defaults.OutputModalities)
	}
	if defaults.InputTranscription == nil || defaults.InputTranscription.Model != "gpt-live-transcribe" {
		t.Fatalf("input transcription = %#v", defaults.InputTranscription)
	}
	if defaults.TurnDetection != nil {
		t.Fatalf("live turn detection = %#v, want none", defaults.TurnDetection)
	}

	segmented, err := NewRealtime("https://api.openai.com/v1", "gpt-transcribe", WithToken("test"))
	if err != nil {
		t.Fatal(err)
	}
	if segmented.Defaults().TurnDetection == nil {
		t.Fatal("gpt-transcribe lost its server VAD default")
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
		InputAudio:          defaults.InputAudio,
		OutputModalities:    defaults.OutputModalities,
		InputTranscription:  &provider.RealtimeTranscription{Model: "alias", Languages: []string{"de", "en"}, Delay: "low"},
		InputNoiseReduction: provider.RealtimeNoiseReductionNear,
		TurnDetection:       &provider.RealtimeTurnDetection{Type: provider.RealtimeTurnDetectionServer, CreateResponse: true},
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
}

func TestTranslateInputTranscriptCarriesFinalText(t *testing.T) {
	session := &openAIRealtimeSession{
		contents:    make(map[string]provider.RealtimeContentType),
		transcripts: make(map[string]bool),
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
		transcripts: make(map[string]bool),
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
