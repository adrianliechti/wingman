package realtime

import (
	"encoding/base64"
	"encoding/json/jsontext"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

// newFakeTranscriber declares no turn detection, like a live transcriber.
func newFakeTranscriber() *fakeRealtime {
	transcriber := newFakeRealtime()
	transcriber.capabilities.TranscriptionOnly = true
	transcriber.defaults = &provider.RealtimeOptions{
		Input: provider.RealtimeInputOptions{
			TurnDetection: &provider.RealtimeTurnDetection{Type: provider.RealtimeTurnDetectionNone},
		},
	}
	return transcriber
}

func openTestTranscription(t *testing.T, transcriber, segmented provider.Realtime) *websocket.Conn {
	t.Helper()
	cfg := &config.Config{}
	cfg.RegisterRealtime("test-transcribe", transcriber)
	cfg.RegisterRealtime("test-segmented", segmented)
	router := chi.NewRouter()
	New(cfg).Attach(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/realtime?intent=transcription", nil)
	if err != nil {
		t.Fatalf("dial realtime: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func transcriptionUpdate(input map[string]any) map[string]any {
	return map[string]any{
		"type":    "session.update",
		"session": map[string]any{"type": "transcription", "audio": map[string]any{"input": input}},
	}
}

func sessionInput(t *testing.T, event map[string]any) map[string]any {
	t.Helper()
	session, _ := event["session"].(map[string]any)
	audio, _ := session["audio"].(map[string]any)
	input, ok := audio["input"].(map[string]any)
	if !ok || len(audio) != 1 {
		t.Fatalf("%v session.audio = %#v, want input only", event["type"], audio)
	}
	return input
}

func appendTestAudio(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	writeWireEvent(t, conn, map[string]any{
		"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(make([]byte, 4800)),
	})
}

func TestTranscriptionSessionStreamsPartialTranscripts(t *testing.T) {
	transcriber := newFakeTranscriber()
	conn := openTestTranscription(t, transcriber, newFakeRealtime())

	created := readWireEvent(t, conn)
	assertEventType(t, created, "session.created")
	session := created["session"].(map[string]any)
	if session["type"] != "transcription" || session["object"] != "realtime.transcription_session" {
		t.Fatalf("session = %#v, want a transcription session", session)
	}
	for _, field := range []string{"model", "instructions", "tools", "output_modalities"} {
		if _, ok := session[field]; ok {
			t.Errorf("transcription session exposes %q", field)
		}
	}
	input := sessionInput(t, created)
	if input["transcription"] != nil {
		t.Errorf("initial transcription = %#v, want null", input["transcription"])
	}
	if detection, _ := input["turn_detection"].(map[string]any); len(detection) != 1 || detection["type"] != "server_vad" {
		t.Errorf("initial turn_detection = %#v, want server_vad", input["turn_detection"])
	}

	// No conversation.created: the update is acknowledged next.
	writeWireEvent(t, conn, transcriptionUpdate(map[string]any{
		"transcription": map[string]any{"model": "test-transcribe", "languages": []string{"de", "en"}, "delay": "low"},
	}))
	updated := readWireEvent(t, conn)
	assertEventType(t, updated, "session.updated")
	input = sessionInput(t, updated)
	transcription, _ := input["transcription"].(map[string]any)
	if transcription["model"] != "test-transcribe" || len(transcription["languages"].([]any)) != 2 {
		t.Fatalf("transcription = %#v", input["transcription"])
	}
	if input["turn_detection"] != nil {
		t.Errorf("turn_detection = %#v, want the transcriber's default (none)", input["turn_detection"])
	}

	// Audio reaches the provider before any commit although turn detection is off.
	appendTestAudio(t, conn)
	waitSignal(t, transcriber.session.audioSent, "streamed transcription audio")

	transcriber.mu.Lock()
	options := transcriber.options[0]
	transcriber.mu.Unlock()
	if got := options.Input.Transcription; got == nil || got.Model != "test-transcribe" || got.Delay != "low" || strings.Join(got.Languages, ",") != "de,en" {
		t.Fatalf("provider transcription options = %#v", got)
	}
	// The client left turn detection to the transcriber.
	if options.Input.TurnDetection != nil || len(options.Output.Modalities) != 1 || options.Output.Modalities[0] != provider.RealtimeModalityText {
		t.Fatalf("provider options = %#v, want text-only transcription with the transcriber's turn detection", options)
	}

	// A delta before the commit announces no item yet.
	transcriber.session.emit(
		provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ContentID: "item_a:0", ItemID: "item_a", Role: provider.MessageRoleUser},
		provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ContentID: "item_a:0", Text: " Guten"},
	)
	delta := readWireEvent(t, conn)
	assertEventType(t, delta, "conversation.item.input_audio_transcription.delta")
	if delta["item_id"] != "item_a" || delta["delta"] != " Guten" {
		t.Fatalf("delta = %#v", delta)
	}

	writeWireEvent(t, conn, map[string]any{"type": "input_audio_buffer.commit"})
	waitSignal(t, transcriber.session.committed, "transcription commit")
	transcriber.session.emit(provider.RealtimeEvent{Type: provider.RealtimeEventInputCommitted, ItemID: "item_a"})
	committed := readWireEvent(t, conn)
	assertEventType(t, committed, "input_audio_buffer.committed")
	if committed["item_id"] != "item_a" {
		t.Fatalf("committed = %#v", committed)
	}
	assertEventType(t, readWireEvent(t, conn), "conversation.item.created")
	assertEventType(t, readWireEvent(t, conn), "conversation.item.added")
	assertEventType(t, readWireEvent(t, conn), "conversation.item.done")

	// The next turn starts before the first transcript is final; each keeps
	// its item, and the provider's final transcript wins over the deltas.
	transcriber.session.emit(
		provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ContentID: "item_b:0", ItemID: "item_b", Role: provider.MessageRoleUser},
		provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ContentID: "item_b:0", Text: " The"},
		provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ContentID: "item_a:0", Text: " Morgen."},
		provider.RealtimeEvent{Type: provider.RealtimeEventContentDone, ContentID: "item_a:0", Text: "Guten Morgen."},
	)
	if next := readWireEvent(t, conn); next["item_id"] != "item_b" || next["delta"] != " The" {
		t.Fatalf("overlapping delta = %#v, want item_b", next)
	}
	if next := readWireEvent(t, conn); next["item_id"] != "item_a" || next["delta"] != " Morgen." {
		t.Fatalf("trailing delta = %#v, want item_a", next)
	}
	completed := readWireEvent(t, conn)
	assertEventType(t, completed, "conversation.item.input_audio_transcription.completed")
	if completed["item_id"] != "item_a" || completed["transcript"] != "Guten Morgen." {
		t.Fatalf("completed = %#v", completed)
	}

	// Without a provider commit the item is announced before its transcript,
	// and a model turn stays hidden.
	transcriber.session.emit(
		provider.RealtimeEvent{Type: provider.RealtimeEventResponseStarted, ResponseID: "resp_hidden"},
		provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ResponseID: "resp_hidden", ContentID: "assistant", Text: "ignored"},
		provider.RealtimeEvent{Type: provider.RealtimeEventResponseDone, ResponseID: "resp_hidden", StopReason: "completed"},
		provider.RealtimeEvent{Type: provider.RealtimeEventContentDone, ContentID: "item_b:0"},
	)
	committed = readWireEvent(t, conn)
	assertEventType(t, committed, "input_audio_buffer.committed")
	if committed["item_id"] != "item_b" {
		t.Fatalf("fallback committed = %#v", committed)
	}
	assertEventType(t, readWireEvent(t, conn), "conversation.item.created")
	assertEventType(t, readWireEvent(t, conn), "conversation.item.added")
	assertEventType(t, readWireEvent(t, conn), "conversation.item.done")
	completed = readWireEvent(t, conn)
	assertEventType(t, completed, "conversation.item.input_audio_transcription.completed")
	if completed["item_id"] != "item_b" || completed["transcript"] != " The" {
		t.Fatalf("second completed = %#v", completed)
	}

	// A reused id of a finished turn starts a new item.
	transcriber.session.emit(
		provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ContentID: "reused", ItemID: "item_b", Role: provider.MessageRoleUser},
		provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ContentID: "reused", Text: "Next"},
	)
	if id, _ := readWireEvent(t, conn)["item_id"].(string); id == "" || id == "item_b" {
		t.Fatalf("reused item id = %q, want a new item", id)
	}
}

func TestTranscriptionSessionTurnDetectionDefaults(t *testing.T) {
	conn := openTestTranscription(t, newFakeTranscriber(), newFakeRealtime())
	assertEventType(t, readWireEvent(t, conn), "session.created")

	// The model's default applies until the client chooses.
	writeWireEvent(t, conn, transcriptionUpdate(map[string]any{"transcription": map[string]any{"model": "test-segmented"}}))
	if detection, _ := sessionInput(t, readWireEvent(t, conn))["turn_detection"].(map[string]any); len(detection) != 1 || detection["type"] != "server_vad" {
		t.Fatalf("turn_detection = %#v, want server_vad without response control", detection)
	}

	writeWireEvent(t, conn, transcriptionUpdate(map[string]any{"turn_detection": map[string]any{"type": "server_vad", "silence_duration_ms": 400}}))
	assertEventType(t, readWireEvent(t, conn), "session.updated")

	writeWireEvent(t, conn, transcriptionUpdate(map[string]any{"transcription": map[string]any{"model": "test-transcribe"}}))
	input := sessionInput(t, readWireEvent(t, conn))
	if detection, _ := input["turn_detection"].(map[string]any); detection["silence_duration_ms"] != float64(400) {
		t.Fatalf("turn_detection = %#v, want the client's choice to survive the model change", input["turn_detection"])
	}
}

func TestTranscriptionSessionModelSwitch(t *testing.T) {
	first, second := newFakeTranscriber(), newFakeTranscriber()
	conn := openTestTranscription(t, first, second)
	assertEventType(t, readWireEvent(t, conn), "session.created")

	writeWireEvent(t, conn, transcriptionUpdate(map[string]any{"transcription": map[string]any{"model": "test-transcribe"}}))
	assertEventType(t, readWireEvent(t, conn), "session.updated")
	// Selection can change before any audio opens a provider stream.
	writeWireEvent(t, conn, transcriptionUpdate(map[string]any{"transcription": map[string]any{"model": "test-segmented", "languages": []string{"en"}}}))
	transcription, _ := sessionInput(t, readWireEvent(t, conn))["transcription"].(map[string]any)
	if transcription["model"] != "test-segmented" {
		t.Fatalf("transcription = %#v, want the new model", transcription)
	}
	first.mu.Lock()
	firstCalls := len(first.options)
	first.mu.Unlock()
	if firstCalls != 0 {
		t.Fatal("the initial model opened a stream before receiving audio")
	}

	appendTestAudio(t, conn)
	waitSignal(t, second.session.audioSent, "audio for the second model")
	second.mu.Lock()
	options := second.options[0]
	second.mu.Unlock()
	if got := options.Input.Transcription; got == nil || got.Model != "test-segmented" || strings.Join(got.Languages, ",") != "en" {
		t.Fatalf("second provider transcription options = %#v", got)
	}

	second.session.emit(
		provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ContentID: "item_b:0", ItemID: "item_b", Role: provider.MessageRoleUser},
		provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ContentID: "item_b:0", Text: "Hello"},
	)
	if delta := readWireEvent(t, conn); delta["item_id"] != "item_b" || delta["delta"] != "Hello" {
		t.Fatalf("delta after the switch = %#v", delta)
	}
}

func TestTranscriptionModelSwitchPreservesStartedStream(t *testing.T) {
	for _, tc := range []struct {
		name              string
		updates, activity bool
	}{
		{"openai", true, true},
		{"gemini", false, true},
		{"bedrock", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, second := newFakeTranscriber(), newFakeTranscriber()
			first.capabilities.SessionUpdates = tc.updates
			first.capabilities.InputActivityEvents = tc.activity
			conn := openTestTranscription(t, first, second)
			assertEventType(t, readWireEvent(t, conn), "session.created")
			writeWireEvent(t, conn, transcriptionUpdate(map[string]any{"transcription": map[string]any{"model": "test-transcribe"}}))
			assertEventType(t, readWireEvent(t, conn), "session.updated")
			appendTestAudio(t, conn)
			waitSignal(t, first.session.audioSent, "original provider audio")
			writeWireEvent(t, conn, map[string]any{"type": "input_audio_buffer.commit"})
			waitSignal(t, first.session.committed, "original provider commit")
			if !tc.activity {
				readThrough(t, conn, "conversation.item.done")
			}

			rejectSwitch := func() {
				t.Helper()
				writeWireEvent(t, conn, transcriptionUpdate(map[string]any{"transcription": map[string]any{"model": "test-segmented"}}))
				reply := readWireEvent(t, conn)
				assertEventType(t, reply, "error")
				if message, _ := reply["error"].(map[string]any)["message"].(string); !strings.Contains(message, "open a new session") {
					t.Fatalf("switch rejection = %#v", reply)
				}
			}
			// The switch arrives before any upstream commit acknowledgement or delta.
			rejectSwitch()
			if tc.activity {
				first.session.emit(provider.RealtimeEvent{Type: provider.RealtimeEventInputCommitted, ItemID: "original"})
				readThrough(t, conn, "conversation.item.done")
			}
			first.session.emit(
				provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ContentID: "original:0", ItemID: "original", Role: provider.MessageRoleUser},
				provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ContentID: "original:0", Text: "Hello"},
			)
			assertEventType(t, readWireEvent(t, conn), "conversation.item.input_audio_transcription.delta")
			rejectSwitch()
			first.session.emit(provider.RealtimeEvent{Type: provider.RealtimeEventContentDone, ContentID: "original:0", Text: "Hello."})
			events := readThrough(t, conn, "conversation.item.input_audio_transcription.completed")
			if completed := events[len(events)-1]; completed["transcript"] != "Hello." || completed["item_id"] != "original" {
				t.Fatalf("original transcript lost: %#v", completed)
			}
			// A completed transcript does not guarantee that final usage has arrived.
			rejectSwitch()
			appendTestAudio(t, conn)
			waitSignal(t, first.session.audioSent, "audio after rejected model changes")
			second.mu.Lock()
			calls := len(second.options)
			second.mu.Unlock()
			if calls != 0 {
				t.Fatal("rejected model change opened the second provider")
			}
		})
	}
}

func TestTranscriptionUsageOnWire(t *testing.T) {
	duration := 1250 * time.Millisecond
	for _, mode := range []string{"transcription", "realtime"} {
		for _, tc := range []struct {
			name  string
			usage *provider.RealtimeTranscriptionUsage
			want  any
		}{
			{"tokens", &provider.RealtimeTranscriptionUsage{Tokens: &provider.RealtimeUsage{InputTokens: 100, OutputTokens: 2, InputAudioTokens: 96, InputTextTokens: 4}}, map[string]any{"type": "tokens", "input_tokens": float64(100), "output_tokens": float64(2), "total_tokens": float64(102), "input_token_details": map[string]any{"audio_tokens": float64(96), "text_tokens": float64(4)}}},
			{"duration", &provider.RealtimeTranscriptionUsage{AudioDuration: &duration}, map[string]any{"type": "duration", "seconds": 1.25}},
			{"unknown", &provider.RealtimeTranscriptionUsage{}, nil},
			{"aggregate-provider", nil, nil},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				upstream := newFakeRealtime()
				var conn *websocket.Conn
				if mode == "transcription" {
					conn = openTestTranscription(t, upstream, newFakeTranscriber())
					assertEventType(t, readWireEvent(t, conn), "session.created")
					writeWireEvent(t, conn, transcriptionUpdate(map[string]any{"transcription": map[string]any{"model": "test-transcribe"}}))
				} else {
					conn = openTestRealtime(t, upstream)
					assertEventType(t, readWireEvent(t, conn), "session.created")
					assertEventType(t, readWireEvent(t, conn), "conversation.created")
					writeWireEvent(t, conn, wingmanSessionUpdate(false))
				}
				assertEventType(t, readWireEvent(t, conn), "session.updated")
				appendTestAudio(t, conn)
				waitSignal(t, upstream.session.audioSent, "audio")
				upstream.session.emit(
					provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ContentID: "input", ItemID: "item_input", ContentType: provider.RealtimeContentText, Role: provider.MessageRoleUser},
					provider.RealtimeEvent{Type: provider.RealtimeEventContentDone, ContentID: "input", Text: "Hello", TranscriptionUsage: tc.usage},
				)
				events := readThrough(t, conn, "conversation.item.input_audio_transcription.completed")
				if got := events[len(events)-1]["usage"]; !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("wire usage = %#v, want %#v", got, tc.want)
				}
			})
		}
	}
}

func TestTranscriptionSessionRejections(t *testing.T) {
	transcriber := newFakeTranscriber()
	conn := openTestTranscription(t, transcriber, newFakeRealtime())
	assertEventType(t, readWireEvent(t, conn), "session.created")

	expectError := func(event map[string]any, code, message string) {
		t.Helper()
		writeWireEvent(t, conn, event)
		reply := readWireEvent(t, conn)
		assertEventType(t, reply, "error")
		failure, _ := reply["error"].(map[string]any)
		if (code != "" && failure["code"] != code) || !strings.Contains(failure["message"].(string), message) {
			t.Fatalf("%v error = %#v, want code %q and message containing %q", event["type"], failure, code, message)
		}
	}

	expectError(map[string]any{
		"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(make([]byte, 4800)),
	}, "", "transcription.model must be set")
	expectError(transcriptionUpdate(map[string]any{"transcription": map[string]any{"model": "missing"}}), "", "realtime provider not found: missing")

	expectError(map[string]any{"type": "response.create"}, "response_create_for_transcription_session", "response.create received for transcription session")
	expectError(map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": "hi"}}},
	}, "", "not supported in a transcription session")

	expectError(map[string]any{
		"type": "session.update", "session": map[string]any{"type": "realtime", "instructions": "hi"},
	}, "", "passing a realtime session update to a transcription session is not allowed")
	for _, session := range []map[string]any{
		{"type": "transcription", "instructions": "hi"},
		{"type": "transcription", "include": []string{"item.input_audio_transcription.logprobs"}},
		{"type": "transcription", "audio": map[string]any{"output": map[string]any{"voice": "alloy"}}},
	} {
		expectError(map[string]any{"type": "session.update", "session": session}, "", "not supported")
	}

	// The session survives every rejection.
	writeWireEvent(t, conn, transcriptionUpdate(map[string]any{"transcription": map[string]any{"model": "test-transcribe"}}))
	assertEventType(t, readWireEvent(t, conn), "session.updated")
}

func TestRealtimeSessionRejectsTranscriptionUpdate(t *testing.T) {
	conn := openTestRealtime(t, newFakeRealtime())
	assertEventType(t, readWireEvent(t, conn), "session.created")
	assertEventType(t, readWireEvent(t, conn), "conversation.created")

	writeWireEvent(t, conn, transcriptionUpdate(map[string]any{"transcription": map[string]any{"model": "gpt-live-transcribe"}}))
	reply := readWireEvent(t, conn)
	assertEventType(t, reply, "error")
	if message, _ := reply["error"].(map[string]any)["message"].(string); !strings.Contains(message, "passing a transcription session update to a realtime session is not allowed") {
		t.Fatalf("error message = %q", message)
	}
}

func TestRealtimeTranscriptionFieldParsing(t *testing.T) {
	config := newSessionConfig(newFakeRealtime().Defaults())
	if err := config.apply(jsontext.Value(`{"type":"realtime","audio":{"input":{"transcription":{"languages":["de","en"],"delay":"low"}}}}`)); err != nil {
		t.Fatal(err)
	}
	transcription := config.options(nil).Input.Transcription
	if transcription == nil || transcription.Model != "gpt-live-transcribe" || transcription.Delay != "low" || strings.Join(transcription.Languages, ",") != "de,en" {
		t.Fatalf("conversation transcription = %#v", transcription)
	}

	for raw, want := range map[string]string{
		`{"type":"realtime","audio":{"input":{"transcription":"gpt-live-transcribe"}}}`: "must be an object or null",
		`{"type":"translation"}`: `session.type must be "realtime" or "transcription"`,
	} {
		config := newSessionConfig(newFakeRealtime().Defaults())
		if err := config.apply(jsontext.Value(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("session %s error = %v, want %q", raw, err, want)
		}
	}
}

// Like MAI-Transcribe behind the OpenAI provider: the service decides.
func TestTranscriptionSessionLeavesTurnDetectionToTheService(t *testing.T) {
	transcriber := newFakeTranscriber()
	transcriber.defaults = &provider.RealtimeOptions{}
	conn := openTestTranscription(t, transcriber, newFakeRealtime())
	assertEventType(t, readWireEvent(t, conn), "session.created")

	writeWireEvent(t, conn, transcriptionUpdate(map[string]any{"transcription": map[string]any{"model": "test-transcribe"}}))
	if detection, _ := sessionInput(t, readWireEvent(t, conn))["turn_detection"].(map[string]any); detection["type"] != "server_vad" {
		t.Fatalf("turn_detection = %#v, want the protocol default", detection)
	}

	appendTestAudio(t, conn)
	waitSignal(t, transcriber.session.audioSent, "transcription audio")
	transcriber.mu.Lock()
	options := transcriber.options[0]
	transcriber.mu.Unlock()
	if options.Input.TurnDetection != nil {
		t.Fatalf("provider turn detection = %#v, want unset", options.Input.TurnDetection)
	}
}

func TestSessionTurnDetectionPassesThrough(t *testing.T) {
	// A provider that declares no default leaves turns to its service, which
	// must not read as manual turns.
	if detection := newSessionConfig(provider.RealtimeOptions{}).TurnDetection; detection == nil || detection.Type != provider.RealtimeTurnDetectionServer || !detection.CreateResponse {
		t.Fatalf("undeclared turn detection = %#v, want the protocol's server VAD", detection)
	}

	disabled := &provider.RealtimeTurnDetection{Type: provider.RealtimeTurnDetectionNone}
	for _, tc := range []struct {
		name   string
		config sessionConfig
		update string
		want   *provider.RealtimeTurnDetection
	}{
		{"conversation unset", newSessionConfig(newFakeRealtime().Defaults()), `{"type":"realtime","instructions":"Hi"}`, nil},
		{"conversation disabled", newSessionConfig(newFakeRealtime().Defaults()), `{"type":"realtime","audio":{"input":{"turn_detection":null}}}`, disabled},
		{"legacy disabled", newSessionConfig(newFakeRealtime().Defaults()), `{"turn_detection":null}`, disabled},
		{"semantic", newSessionConfig(newFakeRealtime().Defaults()), `{"type":"realtime","audio":{"input":{"turn_detection":{"type":"semantic_vad","eagerness":"low"}}}}`,
			&provider.RealtimeTurnDetection{Type: provider.RealtimeTurnDetectionSemantic, Eagerness: "low", CreateResponse: true, InterruptResponse: true}},
		{"transcription unset", newTranscriptionConfig(), `{"type":"transcription","audio":{"input":{"transcription":{"model":"m"}}}}`, nil},
		{"transcription disabled", newTranscriptionConfig(), `{"type":"transcription","audio":{"input":{"turn_detection":null}}}`, disabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := tc.config
			if err := config.apply(jsontext.Value(tc.update)); err != nil {
				t.Fatal(err)
			}
			if got := config.options(nil).Input.TurnDetection; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("provider turn detection = %#v, want %#v", got, tc.want)
			}
		})
	}
}
