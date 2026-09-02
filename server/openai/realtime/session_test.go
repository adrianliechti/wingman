package realtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

// These tests exercise the exact event subset consumed by wingman-chat. They
// deliberately use a semantic fake rather than a fake OpenAI upstream: this is
// the protocol contract every native provider (OpenAI, Gemini Live, Nova Sonic)
// must satisfy after translation.

func TestWingmanChatRealtimeNormalAndMixedInputContract(t *testing.T) {
	upstream := newFakeRealtime()
	conn := openTestRealtime(t, upstream)

	created := readWireEvent(t, conn)
	assertEventType(t, created, "session.created")
	assertNonEmptyString(t, created, "event_id")
	assertNestedString(t, created, "session", "id")

	conversation := readWireEvent(t, conn)
	assertEventType(t, conversation, "conversation.created")
	assertNestedString(t, conversation, "conversation", "id")

	writeWireEvent(t, conn, wingmanSessionUpdate(false))
	updated := readWireEvent(t, conn)
	assertEventType(t, updated, "session.updated")
	assertSessionUpdate(t, updated)

	writeWireEvent(t, conn, map[string]any{
		"type":             "conversation.item.create",
		"previous_item_id": "item_before_text",
		"item": map[string]any{
			"type": "message", "role": "user",
			"content": []map[string]any{{"type": "input_text", "text": "Say hello."}},
		},
	})
	createdItem := readWireEvent(t, conn)
	assertEventType(t, createdItem, "conversation.item.created")
	if got := createdItem["previous_item_id"]; got != "item_before_text" {
		t.Errorf("previous_item_id = %v, want item_before_text", got)
	}
	assertEventType(t, readWireEvent(t, conn), "conversation.item.added")
	assertEventType(t, readWireEvent(t, conn), "conversation.item.done")

	writeWireEvent(t, conn, map[string]any{"type": "response.create"})
	waitSignal(t, upstream.session.responded, "text response.create")
	if got := upstream.session.messagesSnapshot(); len(got) != 1 || got[0].Text() != "Say hello." {
		t.Fatalf("provider messages = %#v, want one current text turn", got)
	}

	emitAudioResponse(upstream.session, "resp_text", "item_text", "content_text", "Hello.")
	textTrace := readThrough(t, conn, "response.done")
	assertSubsequence(t, textTrace,
		"response.created",
		"conversation.item.added",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_audio_transcript.delta",
		"response.output_audio.delta",
		"response.output_audio.done",
		"response.output_audio_transcript.done",
		"response.content_part.done",
		"response.output_item.done",
		"conversation.item.done",
		"response.done",
	)
	assertAudioDelta(t, findEvent(t, textTrace, "response.output_audio.delta"))
	assertCompletedResponse(t, findEvent(t, textTrace, "response.done"), 1)

	// The same connection now takes a voice turn. Native input-activity events
	// are surfaced without synthetic duplicates, and the asynchronous transcript
	// remains a complete OpenAI conversation item.
	audio := []byte{0x01, 0x00, 0x02, 0x00, 0x03, 0x00, 0x04, 0x00}
	writeWireEvent(t, conn, map[string]any{
		"type":  "input_audio_buffer.append",
		"audio": base64.StdEncoding.EncodeToString(audio),
	})
	waitSignal(t, upstream.session.audioSent, "audio append")
	upstream.session.emit(provider.RealtimeEvent{
		Type: provider.RealtimeEventInputSpeechStarted, ItemID: "item_voice", AudioStart: 10 * time.Millisecond,
	})
	assertEventType(t, readWireEvent(t, conn), "input_audio_buffer.speech_started")

	writeWireEvent(t, conn, map[string]any{"type": "input_audio_buffer.commit"})
	waitSignal(t, upstream.session.committed, "audio commit")
	upstream.session.emit(
		provider.RealtimeEvent{Type: provider.RealtimeEventInputSpeechStopped, ItemID: "item_voice", AudioEnd: 250 * time.Millisecond},
		provider.RealtimeEvent{Type: provider.RealtimeEventInputCommitted, ItemID: "item_voice", PreviousItemID: "item_text"},
		provider.RealtimeEvent{Type: provider.RealtimeEventInputTranscriptionPreview, Text: "H"},
		provider.RealtimeEvent{Type: provider.RealtimeEventInputTranscriptionPreview, Text: "Hello, revised"},
		provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ContentID: "input_voice", ItemID: "item_voice", ContentType: provider.RealtimeContentText, Role: provider.MessageRoleUser},
		provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ContentID: "input_voice", ItemID: "item_voice", Text: "Hi"},
		provider.RealtimeEvent{Type: provider.RealtimeEventContentDone, ContentID: "input_voice", ItemID: "item_voice", ContentType: provider.RealtimeContentText},
	)
	inputTrace := readThrough(t, conn, "conversation.item.done")
	assertSubsequence(t, inputTrace,
		"input_audio_buffer.speech_stopped",
		"input_audio_buffer.committed",
		"conversation.item.added",
		"conversation.item.input_audio_transcription.delta",
		"conversation.item.input_audio_transcription.completed",
		"conversation.item.done",
	)
	transcript := findEvent(t, inputTrace, "conversation.item.input_audio_transcription.completed")
	if got, _ := transcript["transcript"].(string); got != "Hi" {
		t.Errorf("transcript = %q, want Hi", got)
	}
	if got := countEvents(inputTrace, "conversation.item.input_audio_transcription.delta"); got != 1 {
		t.Errorf("transcription delta count = %d, want only the finalized delta", got)
	}

	writeWireEvent(t, conn, map[string]any{"type": "response.create"})
	waitSignal(t, upstream.session.responded, "voice response.create")
	emitAudioResponse(upstream.session, "resp_voice", "item_voice_answer", "content_voice_answer", "Hi there.")
	voiceTrace := readThrough(t, conn, "response.done")
	assertAudioDelta(t, findEvent(t, voiceTrace, "response.output_audio.delta"))
	assertCompletedResponse(t, findEvent(t, voiceTrace, "response.done"), 1)

	writeWireEvent(t, conn, map[string]any{
		"type": "conversation.item.truncate", "item_id": "item_voice_answer",
		"content_index": 0, "audio_end_ms": 125,
	})
	waitSignal(t, upstream.session.truncated, "conversation truncate")
	truncated := readWireEvent(t, conn)
	assertEventType(t, truncated, "conversation.item.truncated")
	if got := int(truncated["audio_end_ms"].(float64)); got != 125 {
		t.Errorf("audio_end_ms = %d, want 125", got)
	}

	// OpenAI GA intentionally rejects simultaneous text+audio output. Audio
	// already includes a transcript; text-only is selected on a separate turn.
	writeWireEvent(t, conn, map[string]any{
		"type":    "session.update",
		"session": map[string]any{"type": "realtime", "output_modalities": []string{"audio", "text"}},
	})
	rejected := readWireEvent(t, conn)
	assertEventType(t, rejected, "error")
}

func TestWingmanChatRealtimeToolsAndMultipleOutputItems(t *testing.T) {
	upstream := newFakeRealtime()
	conn := openTestRealtime(t, upstream)
	assertEventType(t, readWireEvent(t, conn), "session.created")
	assertEventType(t, readWireEvent(t, conn), "conversation.created")

	writeWireEvent(t, conn, wingmanSessionUpdate(true))
	assertEventType(t, readWireEvent(t, conn), "session.updated")

	writeWireEvent(t, conn, map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type": "message", "role": "user",
			"content": []map[string]any{{"type": "input_text", "text": "Weather in New York and Zurich?"}},
		},
	})
	assertEventType(t, readWireEvent(t, conn), "conversation.item.created")
	assertEventType(t, readWireEvent(t, conn), "conversation.item.added")
	assertEventType(t, readWireEvent(t, conn), "conversation.item.done")
	writeWireEvent(t, conn, map[string]any{"type": "response.create"})
	waitSignal(t, upstream.session.responded, "tool response.create")

	upstream.session.emit(
		provider.RealtimeEvent{Type: provider.RealtimeEventResponseStarted, ResponseID: "resp_tools"},
		provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ResponseID: "resp_tools", ContentID: "preface", ItemID: "item_preface", ContentType: provider.RealtimeContentAudio, Role: provider.MessageRoleAssistant},
		provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ResponseID: "resp_tools", ContentID: "preface", ItemID: "item_preface", Text: "I’ll check both."},
		provider.RealtimeEvent{Type: provider.RealtimeEventAudioDelta, ResponseID: "resp_tools", ContentID: "preface", ItemID: "item_preface", Audio: []byte{1, 2, 3, 4}},
		provider.RealtimeEvent{Type: provider.RealtimeEventContentDone, ResponseID: "resp_tools", ContentID: "preface", ItemID: "item_preface", ContentType: provider.RealtimeContentAudio},
		provider.RealtimeEvent{Type: provider.RealtimeEventToolCall, ResponseID: "resp_tools", ContentID: "tool_nyc", ItemID: "item_tool_nyc", ToolCall: &provider.ToolCall{ID: "call_nyc", Name: "get_weather", Arguments: `{"city":"New York"}`}},
		provider.RealtimeEvent{Type: provider.RealtimeEventToolCall, ResponseID: "resp_tools", ContentID: "tool_zrh", ItemID: "item_tool_zrh", ToolCall: &provider.ToolCall{ID: "call_zrh", Name: "get_weather", Arguments: `{"city":"Zurich"}`}},
		provider.RealtimeEvent{Type: provider.RealtimeEventUsage, ResponseID: "resp_tools", Usage: &provider.RealtimeUsage{InputTokens: 11, OutputTokens: 7, InputAudioTokens: 4, InputTextTokens: 7, OutputAudioTokens: 3, OutputTextTokens: 4}},
		provider.RealtimeEvent{Type: provider.RealtimeEventResponseDone, ResponseID: "resp_tools", StopReason: "tool_use"},
	)
	trace := readThrough(t, conn, "response.done")
	if got := countEvents(trace, "response.function_call_arguments.delta"); got != 2 {
		t.Fatalf("function argument delta count = %d, want 2\n%s", got, formatTrace(trace))
	}
	if got := countEvents(trace, "response.output_item.done"); got != 3 {
		t.Fatalf("output item done count = %d, want assistant + two tools\n%s", got, formatTrace(trace))
	}
	responseDone := findEvent(t, trace, "response.done")
	assertCompletedResponse(t, responseDone, 3)
	response := responseDone["response"].(map[string]any)
	usage := response["usage"].(map[string]any)
	if got := int(usage["total_tokens"].(float64)); got != 18 {
		t.Errorf("usage.total_tokens = %d, want 18", got)
	}

	for _, result := range []struct {
		id     string
		output string
	}{{"call_nyc", `{"temperature":22}`}, {"call_zrh", `{"temperature":18}`}} {
		writeWireEvent(t, conn, map[string]any{
			"type": "conversation.item.create",
			"item": map[string]any{"type": "function_call_output", "call_id": result.id, "output": result.output},
		})
		waitSignal(t, upstream.session.toolResultSent, "tool result")
		assertEventType(t, readWireEvent(t, conn), "conversation.item.created")
		assertEventType(t, readWireEvent(t, conn), "conversation.item.added")
		assertEventType(t, readWireEvent(t, conn), "conversation.item.done")
	}
	results := upstream.session.toolResultsSnapshot()
	if len(results) != 2 || results[0].id != "call_nyc" || results[1].id != "call_zrh" {
		t.Fatalf("provider tool results = %#v", results)
	}

	writeWireEvent(t, conn, map[string]any{"type": "response.create"})
	waitSignal(t, upstream.session.responded, "post-tool response.create")
	emitAudioResponse(upstream.session, "resp_after_tools", "item_after_tools", "content_after_tools", "New York is 22 degrees and Zurich is 18.")
	followup := readThrough(t, conn, "response.done")
	assertCompletedResponse(t, findEvent(t, followup, "response.done"), 1)

	// Session changes after connection are propagated for providers that expose
	// mutable sessions, which is what wingman-chat's updateSession() relies on.
	writeWireEvent(t, conn, map[string]any{
		"type":    "session.update",
		"session": map[string]any{"type": "realtime", "instructions": "Be even shorter.", "tools": []any{}},
	})
	waitSignal(t, upstream.session.updated, "provider session update")
	assertEventType(t, readWireEvent(t, conn), "session.updated")
}

func TestWingmanChatRealtimeTranscriptionFailureContract(t *testing.T) {
	upstream := newFakeRealtime()
	conn := openTestRealtime(t, upstream)
	assertEventType(t, readWireEvent(t, conn), "session.created")
	assertEventType(t, readWireEvent(t, conn), "conversation.created")
	writeWireEvent(t, conn, wingmanSessionUpdate(false))
	assertEventType(t, readWireEvent(t, conn), "session.updated")

	writeWireEvent(t, conn, map[string]any{
		"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString([]byte{0, 0, 0, 0}),
	})
	waitSignal(t, upstream.session.audioSent, "audio append")
	upstream.session.emit(provider.RealtimeEvent{
		Type: provider.RealtimeEventInputTranscriptionFailed, ItemID: "item_bad_audio", Err: errors.New("audio was unintelligible"),
	})
	failed := readWireEvent(t, conn)
	assertEventType(t, failed, "conversation.item.input_audio_transcription.failed")
	if got := failed["item_id"]; got != "item_bad_audio" {
		t.Errorf("item_id = %v, want item_bad_audio", got)
	}
	apiError, ok := failed["error"].(map[string]any)
	if !ok || !strings.Contains(apiError["message"].(string), "unintelligible") {
		t.Fatalf("transcription error = %#v", failed["error"])
	}
}

func TestRealtimeProviderContentFilterErrorContract(t *testing.T) {
	upstream := newFakeRealtime()
	conn := openTestRealtime(t, upstream)
	assertEventType(t, readWireEvent(t, conn), "session.created")
	assertEventType(t, readWireEvent(t, conn), "conversation.created")

	writeWireEvent(t, conn, wingmanSessionUpdate(false))
	assertEventType(t, readWireEvent(t, conn), "session.updated")
	writeWireEvent(t, conn, map[string]any{
		"type":  "input_audio_buffer.append",
		"audio": base64.StdEncoding.EncodeToString([]byte{0, 0, 0, 0}),
	})
	waitSignal(t, upstream.session.audioSent, "provider connection")

	upstream.session.emit(provider.RealtimeEvent{
		Type: provider.RealtimeEventError,
		Error: &provider.RealtimeError{
			Type: "invalid_request_error", Code: "content_filter",
			Message: "The provider rejected session context; this may be a false positive.",
		},
		Err: errors.New("ValidationException: RequestId=secret-upstream-id"),
	})

	event := readWireEvent(t, conn)
	assertEventType(t, event, "error")
	apiError, ok := event["error"].(map[string]any)
	if !ok {
		t.Fatalf("error = %#v, want object", event["error"])
	}
	if got := apiError["type"]; got != "invalid_request_error" {
		t.Errorf("error.type = %v", got)
	}
	if got := apiError["code"]; got != "content_filter" {
		t.Errorf("error.code = %v", got)
	}
	message, _ := apiError["message"].(string)
	if !strings.Contains(message, "false positive") {
		t.Errorf("error.message = %q, want actionable explanation", message)
	}
	if strings.Contains(message, "RequestId") || strings.Contains(message, "ValidationException") {
		t.Errorf("error.message leaks upstream diagnostics: %q", message)
	}
}

func TestRealtimeProviderDiagnosticErrorIsNotExposed(t *testing.T) {
	upstream := newFakeRealtime()
	conn := openTestRealtime(t, upstream)
	assertEventType(t, readWireEvent(t, conn), "session.created")
	assertEventType(t, readWireEvent(t, conn), "conversation.created")

	writeWireEvent(t, conn, map[string]any{
		"type":  "input_audio_buffer.append",
		"audio": base64.StdEncoding.EncodeToString([]byte{0, 0}),
	})
	waitSignal(t, upstream.session.audioSent, "provider connection")
	upstream.session.emit(provider.RealtimeEvent{
		Type: provider.RealtimeEventError,
		Err:  errors.New("ValidationException: RequestId=secret-upstream-id"),
	})

	event := readWireEvent(t, conn)
	assertEventType(t, event, "error")
	apiError := event["error"].(map[string]any)
	message, _ := apiError["message"].(string)
	if message != "The realtime provider reported an error" {
		t.Errorf("error.message = %q, want generic provider error", message)
	}
	if strings.Contains(message, "RequestId") || strings.Contains(message, "ValidationException") {
		t.Errorf("error.message leaks upstream diagnostics: %q", message)
	}
}

func TestRealtimeFailedResponseAndMetadataContract(t *testing.T) {
	upstream := newFakeRealtime()
	conn := openTestRealtime(t, upstream)
	assertEventType(t, readWireEvent(t, conn), "session.created")
	assertEventType(t, readWireEvent(t, conn), "conversation.created")

	writeWireEvent(t, conn, map[string]any{
		"type":  "input_audio_buffer.append",
		"audio": base64.StdEncoding.EncodeToString([]byte{0, 0}),
	})
	waitSignal(t, upstream.session.audioSent, "provider connection")
	writeWireEvent(t, conn, map[string]any{
		"type": "response.create",
		"response": map[string]any{
			"metadata": map[string]any{"request": "weather"},
		},
	})
	waitSignal(t, upstream.session.responded, "response.create")
	upstream.session.emit(
		provider.RealtimeEvent{Type: provider.RealtimeEventResponseStarted, ResponseID: "resp_failed"},
		provider.RealtimeEvent{Type: provider.RealtimeEventResponseDone, ResponseID: "resp_failed", StopReason: "failed"},
	)

	trace := readThrough(t, conn, "response.done")
	for _, eventType := range []string{"response.created", "response.done"} {
		response := findEvent(t, trace, eventType)["response"].(map[string]any)
		metadata := response["metadata"].(map[string]any)
		if got := metadata["request"]; got != "weather" {
			t.Errorf("%s metadata.request = %v, want weather", eventType, got)
		}
	}

	response := findEvent(t, trace, "response.done")["response"].(map[string]any)
	if got := response["status"]; got != "failed" {
		t.Errorf("response status = %v, want failed", got)
	}
	details := response["status_details"].(map[string]any)
	if got := details["type"]; got != "failed" {
		t.Errorf("status_details.type = %v, want failed", got)
	}
}

func TestRealtimeAudioFormatParsing(t *testing.T) {
	current := provider.RealtimeAudioFormat{
		Encoding: provider.RealtimeAudioPCM, SampleRate: 16000, SampleSize: 16, Channels: 1,
	}
	tests := []struct {
		name string
		raw  string
		want provider.RealtimeAudioFormat
	}{
		{"PCM object", `{"type":"audio/pcm","rate":24000}`, provider.RealtimeAudioFormat{Encoding: provider.RealtimeAudioPCM, SampleRate: 24000, SampleSize: 16, Channels: 1}},
		{"PCM legacy", `"pcm16"`, provider.RealtimeAudioFormat{Encoding: provider.RealtimeAudioPCM, SampleRate: 24000, SampleSize: 16, Channels: 1}},
		{"PCMU object", `{"type":"audio/pcmu"}`, provider.RealtimeAudioFormat{Encoding: provider.RealtimeAudioPCMU, SampleRate: 8000, SampleSize: 8, Channels: 1}},
		{"PCMA legacy", `"g711_alaw"`, provider.RealtimeAudioFormat{Encoding: provider.RealtimeAudioPCMA, SampleRate: 8000, SampleSize: 8, Channels: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseAudioFormat(json.RawMessage(test.raw), current)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("format = %#v, want %#v", got, test.want)
			}
			if err := validateAudioFormat("input", got); err != nil {
				t.Fatalf("canonical format rejected: %v", err)
			}
		})
	}

	invalid, err := parseAudioFormat(json.RawMessage(`{"type":"audio/pcm","rate":16000}`), current)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAudioFormat("input", invalid); err == nil {
		t.Fatal("16000 Hz PCM was accepted")
	}
}

func TestRealtimeToolChoiceParsing(t *testing.T) {
	tests := []struct {
		raw     string
		want    provider.ToolChoice
		wantErr bool
	}{
		{`"auto"`, provider.ToolChoiceAuto, false},
		{`"none"`, provider.ToolChoiceNone, false},
		{`"required"`, provider.ToolChoiceAny, false},
		{`"sometimes"`, "", true},
		{`{"type":"function","name":"weather"}`, "", true},
	}
	for _, test := range tests {
		got, err := parseToolChoice(json.RawMessage(test.raw))
		if (err != nil) != test.wantErr {
			t.Errorf("parseToolChoice(%s) error = %v, wantErr %v", test.raw, err, test.wantErr)
		}
		if got != test.want {
			t.Errorf("parseToolChoice(%s) = %q, want %q", test.raw, got, test.want)
		}
	}
}

func TestRealtimeTruncationAndMetadataParsing(t *testing.T) {
	truncation, err := parseTruncation(json.RawMessage(`"auto"`))
	if err != nil || truncation != nil {
		t.Fatalf("auto truncation = %#v, %v; want nil, nil", truncation, err)
	}
	if _, err := parseTruncation(json.RawMessage(`{"type":"retention_ratio","retention_ratio":1.1}`)); err == nil {
		t.Fatal("out-of-range retention ratio was accepted")
	}

	metadata, err := parseMetadata(json.RawMessage(`{"request":"weather"}`))
	if err != nil || metadata["request"] != "weather" {
		t.Fatalf("metadata = %#v, %v", metadata, err)
	}
	if _, err := parseMetadata(json.RawMessage(`{"request":42}`)); err == nil {
		t.Fatal("non-string metadata value was accepted")
	}
	if _, err := parseMetadata(json.RawMessage(`{"request":"` + strings.Repeat("x", 513) + `"}`)); err == nil {
		t.Fatal("oversized metadata value was accepted")
	}
}

func TestRealtimeSessionRejectsIgnoredFields(t *testing.T) {
	for _, raw := range []string{
		`{"type":"realtime","parallel_tool_calls":true}`,
		`{"type":"realtime","audio":{"output":{"speed":1.25}}}`,
		`{"type":"realtime","audio":{"input":{"transcription":{"model":"gpt-live-transcribe","keywords":["Wingman"]}}}}`,
	} {
		config := newSessionConfig(newFakeRealtime().Defaults())
		if err := config.apply(json.RawMessage(raw)); err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Errorf("session %s error = %v, want explicit unsupported-field error", raw, err)
		}
	}
}

func TestProviderBargeInNormalizesOpenAIInputActivity(t *testing.T) {
	upstream := newFakeRealtime()
	upstream.capabilities = provider.RealtimeCapabilities{Interruption: true}
	conn := openTestRealtime(t, upstream)
	assertEventType(t, readWireEvent(t, conn), "session.created")
	assertEventType(t, readWireEvent(t, conn), "conversation.created")
	writeWireEvent(t, conn, wingmanSessionUpdate(false))
	assertEventType(t, readWireEvent(t, conn), "session.updated")

	// Continuous realtime providers receive silence as well as speech. Merely
	// receiving a packet must not be exposed as VAD activity.
	writeWireEvent(t, conn, map[string]any{
		"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString([]byte{0, 0, 0, 0}),
	})
	waitSignal(t, upstream.session.audioSent, "continuous audio append")

	upstream.session.emit(
		provider.RealtimeEvent{Type: provider.RealtimeEventResponseStarted, ResponseID: "resp_barge"},
		provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ResponseID: "resp_barge", ContentID: "assistant_barge", ItemID: "assistant_item", ContentType: provider.RealtimeContentAudio, Role: provider.MessageRoleAssistant},
		provider.RealtimeEvent{Type: provider.RealtimeEventAudioDelta, ResponseID: "resp_barge", ContentID: "assistant_barge", ItemID: "assistant_item", Audio: []byte{1, 2}},
	)
	assertEventType(t, readWireEvent(t, conn), "response.created")
	assertEventType(t, readWireEvent(t, conn), "conversation.item.created")
	assertEventType(t, readWireEvent(t, conn), "conversation.item.added")
	assertEventType(t, readWireEvent(t, conn), "response.output_item.added")
	assertEventType(t, readWireEvent(t, conn), "response.content_part.added")
	assertEventType(t, readWireEvent(t, conn), "response.output_audio.delta")

	upstream.session.emit(provider.RealtimeEvent{Type: provider.RealtimeEventInterrupted, ResponseID: "resp_barge"})
	started := readWireEvent(t, conn)
	assertEventType(t, started, "input_audio_buffer.speech_started")
	startedItemID, _ := started["item_id"].(string)
	if startedItemID == "" {
		t.Fatal("barge-in speech_started has no item_id")
	}

	upstream.session.emit(provider.RealtimeEvent{
		Type: provider.RealtimeEventContentStarted, ContentID: "user_barge", ItemID: "provider_item",
		ContentType: provider.RealtimeContentText, Role: provider.MessageRoleUser,
	})
	stopped := readWireEvent(t, conn)
	committed := readWireEvent(t, conn)
	created := readWireEvent(t, conn)
	added := readWireEvent(t, conn)
	assertEventType(t, stopped, "input_audio_buffer.speech_stopped")
	assertEventType(t, committed, "input_audio_buffer.committed")
	assertEventType(t, created, "conversation.item.created")
	assertEventType(t, added, "conversation.item.added")
	for _, event := range []map[string]any{stopped, committed} {
		if event["item_id"] != startedItemID {
			t.Errorf("%s item_id = %v, want %s", event["type"], event["item_id"], startedItemID)
		}
	}
	item := added["item"].(map[string]any)
	if item["id"] != startedItemID {
		t.Errorf("input conversation item id = %v, want %s", item["id"], startedItemID)
	}
}

func wingmanSessionUpdate(withTools bool) map[string]any {
	session := map[string]any{
		"type":         "realtime",
		"instructions": "Be concise.",
		"truncation": map[string]any{
			"type": "retention_ratio", "retention_ratio": 0.8,
			"token_limits": map[string]any{"post_instructions": 8000},
		},
		"audio": map[string]any{
			"input": map[string]any{
				"format":          map[string]any{"type": "audio/pcm", "rate": 24000},
				"transcription":   map[string]any{"model": "gpt-live-transcribe"},
				"noise_reduction": map[string]any{"type": "far_field"},
				"turn_detection": map[string]any{
					"type": "semantic_vad", "eagerness": "auto",
					"create_response": true, "interrupt_response": true,
				},
			},
			"output": map[string]any{
				"format": map[string]any{"type": "audio/pcm", "rate": 24000}, "voice": "alloy",
			},
		},
	}
	if withTools {
		session["tool_choice"] = "required"
		session["tools"] = []map[string]any{{
			"type": "function", "name": "get_weather", "description": "Get current weather.",
			"parameters": map[string]any{
				"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []string{"city"},
			},
		}}
	}
	return map[string]any{"type": "session.update", "session": session}
}

func emitAudioResponse(session *fakeRealtimeSession, responseID, itemID, contentID, transcript string) {
	session.emit(
		provider.RealtimeEvent{Type: provider.RealtimeEventResponseStarted, ResponseID: responseID},
		provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ResponseID: responseID, ContentID: contentID, ItemID: itemID, ContentType: provider.RealtimeContentAudio, Role: provider.MessageRoleAssistant},
		provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ResponseID: responseID, ContentID: contentID, ItemID: itemID, Text: transcript},
		provider.RealtimeEvent{Type: provider.RealtimeEventAudioDelta, ResponseID: responseID, ContentID: contentID, ItemID: itemID, Audio: []byte{0, 1, 2, 3}},
		provider.RealtimeEvent{Type: provider.RealtimeEventContentDone, ResponseID: responseID, ContentID: contentID, ItemID: itemID, ContentType: provider.RealtimeContentAudio},
		provider.RealtimeEvent{Type: provider.RealtimeEventUsage, ResponseID: responseID, Usage: &provider.RealtimeUsage{InputTokens: 2, OutputTokens: 3, InputTextTokens: 2, OutputTextTokens: 1, OutputAudioTokens: 2}},
		provider.RealtimeEvent{Type: provider.RealtimeEventResponseDone, ResponseID: responseID, StopReason: "completed"},
	)
}

type fakeRealtime struct {
	session      *fakeRealtimeSession
	capabilities provider.RealtimeCapabilities

	mu      sync.Mutex
	options []provider.RealtimeOptions
}

func newFakeRealtime() *fakeRealtime {
	return &fakeRealtime{
		session: newFakeRealtimeSession(),
		capabilities: provider.RealtimeCapabilities{
			SessionUpdates: true, AudioBufferClearing: true, ResponseRequests: true,
			Interruption: true, InputActivityEvents: true,
		},
	}
}

func (f *fakeRealtime) Defaults() provider.RealtimeOptions {
	return provider.RealtimeOptions{
		Voice:            "alloy",
		InputAudio:       provider.RealtimeAudioFormat{Encoding: provider.RealtimeAudioPCM, SampleRate: 24000, SampleSize: 16, Channels: 1},
		OutputAudio:      provider.RealtimeAudioFormat{Encoding: provider.RealtimeAudioPCM, SampleRate: 24000, SampleSize: 16, Channels: 1},
		OutputModalities: []provider.RealtimeModality{provider.RealtimeModalityAudio},
		ToolChoice:       provider.ToolChoiceAuto,
		TurnDetection:    &provider.RealtimeTurnDetection{Type: provider.RealtimeTurnDetectionServer, CreateResponse: true, InterruptResponse: true},
	}
}

func (f *fakeRealtime) Capabilities() provider.RealtimeCapabilities {
	return f.capabilities
}

func (f *fakeRealtime) Connect(_ context.Context, options *provider.RealtimeOptions) (provider.RealtimeSession, error) {
	f.mu.Lock()
	f.options = append(f.options, *options)
	f.mu.Unlock()
	return f.session, nil
}

type recordedToolResult struct {
	id     string
	output string
}

type fakeRealtimeSession struct {
	events chan provider.RealtimeEvent

	responded      chan struct{}
	audioSent      chan struct{}
	committed      chan struct{}
	toolResultSent chan struct{}
	truncated      chan struct{}
	updated        chan struct{}
	interrupted    chan struct{}

	mu          sync.Mutex
	messages    []provider.Message
	toolResults []recordedToolResult
}

func newFakeRealtimeSession() *fakeRealtimeSession {
	return &fakeRealtimeSession{
		events:    make(chan provider.RealtimeEvent, 128),
		responded: make(chan struct{}, 16), audioSent: make(chan struct{}, 16),
		committed: make(chan struct{}, 16), toolResultSent: make(chan struct{}, 16),
		truncated: make(chan struct{}, 16), updated: make(chan struct{}, 16), interrupted: make(chan struct{}, 16),
	}
}

func (s *fakeRealtimeSession) Update(context.Context, *provider.RealtimeOptions) error {
	s.updated <- struct{}{}
	return nil
}

func (s *fakeRealtimeSession) SendAudio(context.Context, []byte) error {
	s.audioSent <- struct{}{}
	return nil
}

func (s *fakeRealtimeSession) CommitAudio(context.Context) error {
	s.committed <- struct{}{}
	return nil
}

func (s *fakeRealtimeSession) ClearAudio(context.Context) error { return nil }

func (s *fakeRealtimeSession) SendMessage(_ context.Context, message provider.Message) error {
	s.mu.Lock()
	s.messages = append(s.messages, message)
	s.mu.Unlock()
	return nil
}

func (s *fakeRealtimeSession) SendToolResult(_ context.Context, id, output string) error {
	s.mu.Lock()
	s.toolResults = append(s.toolResults, recordedToolResult{id: id, output: output})
	s.mu.Unlock()
	s.toolResultSent <- struct{}{}
	return nil
}

func (s *fakeRealtimeSession) TruncateOutput(context.Context, string, time.Duration) error {
	s.truncated <- struct{}{}
	return nil
}

func (s *fakeRealtimeSession) Respond(context.Context, *provider.RealtimeResponseOptions) error {
	s.responded <- struct{}{}
	return nil
}

func (s *fakeRealtimeSession) Interrupt(context.Context) error {
	s.interrupted <- struct{}{}
	return nil
}

func (s *fakeRealtimeSession) Events() <-chan provider.RealtimeEvent { return s.events }
func (s *fakeRealtimeSession) Close() error                          { return nil }

func (s *fakeRealtimeSession) emit(events ...provider.RealtimeEvent) {
	for _, event := range events {
		s.events <- event
	}
}

func (s *fakeRealtimeSession) messagesSnapshot() []provider.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]provider.Message(nil), s.messages...)
}

func (s *fakeRealtimeSession) toolResultsSnapshot() []recordedToolResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedToolResult(nil), s.toolResults...)
}

func openTestRealtime(t *testing.T, upstream provider.Realtime) *websocket.Conn {
	t.Helper()
	cfg := &config.Config{}
	cfg.RegisterRealtime("test-realtime", upstream)
	router := chi.NewRouter()
	New(cfg).Attach(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	target := "ws" + strings.TrimPrefix(server.URL, "http") + "/realtime?model=test-realtime"
	conn, response, err := websocket.DefaultDialer.Dial(target, nil)
	if err != nil {
		if response != nil {
			t.Fatalf("dial realtime: %v (%s)", err, response.Status)
		}
		t.Fatalf("dial realtime: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func writeWireEvent(t *testing.T, conn *websocket.Conn, event any) {
	t.Helper()
	if err := conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(event); err != nil {
		t.Fatalf("write realtime event: %v", err)
	}
}

func readWireEvent(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := conn.ReadJSON(&event); err != nil {
		t.Fatalf("read realtime event: %v", err)
	}
	return event
}

func readThrough(t *testing.T, conn *websocket.Conn, terminal string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for len(events) < 100 {
		event := readWireEvent(t, conn)
		events = append(events, event)
		if event["type"] == terminal {
			return events
		}
	}
	t.Fatalf("did not receive %s\n%s", terminal, formatTrace(events))
	return nil
}

func waitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func assertEventType(t *testing.T, event map[string]any, want string) {
	t.Helper()
	if got := event["type"]; got != want {
		t.Fatalf("event type = %v, want %s: %#v", got, want, event)
	}
	assertNonEmptyString(t, event, "event_id")
}

func assertNonEmptyString(t *testing.T, object map[string]any, key string) {
	t.Helper()
	if value, ok := object[key].(string); !ok || value == "" {
		t.Errorf("%s = %#v, want non-empty string", key, object[key])
	}
}

func assertNestedString(t *testing.T, object map[string]any, objectKey, key string) {
	t.Helper()
	nested, ok := object[objectKey].(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", objectKey, object[objectKey])
	}
	assertNonEmptyString(t, nested, key)
}

func assertSessionUpdate(t *testing.T, event map[string]any) {
	t.Helper()
	session := event["session"].(map[string]any)
	if got := session["instructions"]; got != "Be concise." {
		t.Errorf("session.instructions = %v", got)
	}
	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	turnDetection := input["turn_detection"].(map[string]any)
	if got := turnDetection["type"]; got != "semantic_vad" {
		t.Errorf("turn detection type = %v", got)
	}
	if got := turnDetection["eagerness"]; got != "auto" {
		t.Errorf("turn detection eagerness = %v", got)
	}
}

func assertSubsequence(t *testing.T, events []map[string]any, expected ...string) {
	t.Helper()
	next := 0
	for _, event := range events {
		if next < len(expected) && event["type"] == expected[next] {
			next++
		}
	}
	if next != len(expected) {
		t.Fatalf("missing ordered event %q after %v\n%s", expected[next], expected[:next], formatTrace(events))
	}
}

func findEvent(t *testing.T, events []map[string]any, eventType string) map[string]any {
	t.Helper()
	for _, event := range events {
		if event["type"] == eventType {
			return event
		}
	}
	t.Fatalf("event %s not found\n%s", eventType, formatTrace(events))
	return nil
}

func countEvents(events []map[string]any, eventType string) int {
	count := 0
	for _, event := range events {
		if event["type"] == eventType {
			count++
		}
	}
	return count
}

func assertAudioDelta(t *testing.T, event map[string]any) {
	t.Helper()
	assertNonEmptyString(t, event, "response_id")
	assertNonEmptyString(t, event, "item_id")
	encoded, ok := event["delta"].(string)
	if !ok {
		t.Fatalf("audio delta = %#v, want base64 string", event["delta"])
	}
	if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
		t.Errorf("audio delta is not base64: %v", err)
	}
}

func assertCompletedResponse(t *testing.T, event map[string]any, outputCount int) {
	t.Helper()
	response, ok := event["response"].(map[string]any)
	if !ok {
		t.Fatalf("response = %#v, want object", event["response"])
	}
	assertNonEmptyString(t, response, "id")
	if got := response["status"]; got != "completed" {
		t.Errorf("response status = %v, want completed", got)
	}
	output, ok := response["output"].([]any)
	if !ok || len(output) != outputCount {
		t.Fatalf("response output = %#v, want %d items", response["output"], outputCount)
	}
}

func formatTrace(events []map[string]any) string {
	data, _ := json.MarshalIndent(events, "", "  ")
	return string(data)
}
