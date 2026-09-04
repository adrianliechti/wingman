package bedrock

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

type credentialsProviderFunc func(context.Context) (aws.Credentials, error)

func (f credentialsProviderFunc) Retrieve(ctx context.Context) (aws.Credentials, error) {
	return f(ctx)
}

func TestRealtimeConnectRequiresStandardAWSCredentials(t *testing.T) {
	opened := false
	realtime := &Realtime{
		Config: &Config{model: "amazon.nova-2-sonic-v1:0", voice: defaultRealtimeVoice},
		credentials: credentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{}, errors.New("no credential sources")
		}),
		open: func(context.Context, *bedrockruntime.InvokeModelWithBidirectionalStreamInput) (bidirectionalStream, error) {
			opened = true
			return nil, errors.New("must not open")
		},
	}

	_, err := realtime.Connect(context.Background(), nil)
	if err == nil {
		t.Fatal("Connect succeeded without SigV4 credentials")
	}
	if opened {
		t.Fatal("Connect opened the bidirectional stream before resolving credentials")
	}
	for _, want := range []string{"standard AWS SigV4 credentials", "AWS_ACCESS_KEY_ID", "Bedrock API keys do not support InvokeModelWithBidirectionalStream"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Connect error %q does not contain %q", err, want)
		}
	}
}

func TestNova2ContentFilterErrorIsSafeAndStructured(t *testing.T) {
	upstream := &types.ValidationException{Message: aws.String(
		"RequestId=example: Error 1: This request has been blocked by our content filters.",
	)}
	converted := convertError(upstream)

	if got := provider.TypeFromError(converted); got != "content_filter" {
		t.Fatalf("provider error type = %q, want content_filter", got)
	}
	details := normalizeBedrockRealtimeError(converted)
	if details == nil {
		t.Fatal("normalized realtime error is nil")
	}
	if details.Type != "invalid_request_error" || details.Code != "content_filter" {
		t.Fatalf("normalized realtime error = %#v", details)
	}
	if strings.Contains(details.Message, "RequestId") || strings.Contains(details.Message, "ValidationException") {
		t.Fatalf("client-safe message leaks upstream diagnostics: %q", details.Message)
	}
	if !strings.Contains(details.Message, "false positive") {
		t.Fatalf("client-safe message is not actionable: %q", details.Message)
	}
}

type recordingBidirectionalStream struct {
	inputs []map[string]any
	events chan types.InvokeModelWithBidirectionalStreamOutput
}

func newRecordingBidirectionalStream() *recordingBidirectionalStream {
	return &recordingBidirectionalStream{events: make(chan types.InvokeModelWithBidirectionalStreamOutput)}
}

func (s *recordingBidirectionalStream) Send(_ context.Context, input types.InvokeModelWithBidirectionalStreamInput) error {
	chunk, ok := input.(*types.InvokeModelWithBidirectionalStreamInputMemberChunk)
	if !ok {
		return errors.New("unexpected input union member")
	}
	var event map[string]any
	if err := json.Unmarshal(chunk.Value.Bytes, &event); err != nil {
		return err
	}
	s.inputs = append(s.inputs, event)
	return nil
}

func (s *recordingBidirectionalStream) Events() <-chan types.InvokeModelWithBidirectionalStreamOutput {
	return s.events
}

func (*recordingBidirectionalStream) Close() error { return nil }
func (*recordingBidirectionalStream) Err() error   { return nil }

func TestNova2StartEventSequenceAndToolSchema(t *testing.T) {
	stream := newRecordingBidirectionalStream()
	defaults := (&Realtime{Config: &Config{voice: defaultRealtimeVoice}}).Defaults()
	defaults.Instructions = "Be concise."
	defaults.TurnDetection.Eagerness = "high"
	defaults.History = []provider.Message{
		provider.UserMessage("Earlier question"),
		provider.AssistantMessage("Earlier answer"),
	}
	defaults.Tools = []provider.Tool{{
		Name: "get_weather", Description: "Get the weather",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"location": map[string]any{"type": "string"},
			},
			"required": []any{"location"},
		},
	}}

	session := &realtimeSession{
		stream: stream, options: defaults,
		promptName: "prompt", audioContentName: "audio",
	}
	if err := session.start(context.Background()); err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(stream.inputs))
	for _, input := range stream.inputs {
		event := input["event"].(map[string]any)
		for name := range event {
			names = append(names, name)
		}
	}
	wantNames := []string{
		"sessionStart", "promptStart",
		"contentStart", "textInput", "contentEnd",
		"contentStart", "textInput", "contentEnd",
		"contentStart", "textInput", "contentEnd",
		"contentStart",
	}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("event sequence = %v, want %v", names, wantNames)
	}

	sessionStart := eventPayload(t, stream.inputs[0], "sessionStart")
	turn := sessionStart["turnDetectionConfiguration"].(map[string]any)
	if got := turn["endpointingSensitivity"]; got != "HIGH" {
		t.Errorf("endpointingSensitivity = %v, want HIGH", got)
	}

	promptStart := eventPayload(t, stream.inputs[1], "promptStart")
	toolConfiguration := promptStart["toolConfiguration"].(map[string]any)
	tools := toolConfiguration["tools"].([]any)
	toolSpec := tools[0].(map[string]any)["toolSpec"].(map[string]any)
	inputSchema := toolSpec["inputSchema"].(map[string]any)
	encodedSchema, ok := inputSchema["json"].(string)
	if !ok {
		t.Fatalf("tool inputSchema.json = %#v; want a JSON-encoded string", inputSchema["json"])
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(encodedSchema), &schema); err != nil {
		t.Fatalf("tool inputSchema.json is not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("tool schema type = %v", schema["type"])
	}

	for _, index := range []int{5, 8} {
		historyStart := eventPayload(t, stream.inputs[index], "contentStart")
		if interactive, _ := historyStart["interactive"].(bool); interactive {
			t.Errorf("history contentStart[%d] interactive = true", index)
		}
	}

	audioStart := eventPayload(t, stream.inputs[len(stream.inputs)-1], "contentStart")
	if audioStart["type"] != "AUDIO" || audioStart["interactive"] != true {
		t.Errorf("audio contentStart = %#v", audioStart)
	}
}

func TestNova2SplitsLargeTextInputsWithoutBreakingUTF8(t *testing.T) {
	stream := newRecordingBidirectionalStream()
	session := &realtimeSession{stream: stream, promptName: "prompt"}

	// Put a multi-byte rune across the nominal chunk boundary. The adapter must
	// keep every event under Nova's 50 KB limit while preserving the exact text.
	input := strings.Repeat("a", maxRealtimeTextInputBytes-1) + "🙂" + strings.Repeat("b", maxRealtimeTextInputBytes)
	if err := session.sendTextBlock(context.Background(), provider.SystemMessage(input), false); err != nil {
		t.Fatal(err)
	}

	var chunks []string
	for _, sent := range stream.inputs {
		event := sent["event"].(map[string]any)
		value, ok := event["textInput"]
		if !ok {
			continue
		}
		chunk := value.(map[string]any)["content"].(string)
		if len(chunk) > maxRealtimeTextInputBytes {
			t.Fatalf("textInput chunk has %d bytes, max %d", len(chunk), maxRealtimeTextInputBytes)
		}
		if !utf8.ValidString(chunk) {
			t.Fatalf("textInput chunk is not valid UTF-8")
		}
		chunks = append(chunks, chunk)
	}

	if len(chunks) < 2 {
		t.Fatalf("got %d textInput chunks, want at least 2", len(chunks))
	}
	if got := strings.Join(chunks, ""); got != input {
		t.Fatal("split textInput did not preserve the original text")
	}
}

func eventPayload(t *testing.T, input map[string]any, name string) map[string]any {
	t.Helper()
	event, ok := input["event"].(map[string]any)
	if !ok {
		t.Fatalf("event envelope = %#v", input)
	}
	payload, ok := event[name].(map[string]any)
	if !ok {
		t.Fatalf("event %s = %#v", name, event[name])
	}
	return payload
}

func TestParseNova2OutputEvents(t *testing.T) {
	audio := []byte{0, 1, 2, 3}
	tests := []struct {
		name string
		data map[string]any
		want provider.RealtimeEvent
	}{
		{
			name: "completion start",
			data: map[string]any{"completionStart": map[string]any{"completionId": "completion"}},
			want: provider.RealtimeEvent{Type: provider.RealtimeEventResponseStarted, ResponseID: "completion"},
		},
		{
			name: "user final transcript start",
			data: map[string]any{"contentStart": map[string]any{
				"completionId": "completion", "contentId": "asr", "type": "TEXT", "role": "USER",
				"additionalModelFields": `{"generationStage":"FINAL"}`,
			}},
			want: provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ResponseID: "completion", ContentID: "asr", ContentType: provider.RealtimeContentText, Role: provider.MessageRoleUser, Stage: provider.RealtimeGenerationFinal},
		},
		{
			name: "text",
			data: map[string]any{"textOutput": map[string]any{"completionId": "completion", "contentId": "text", "content": "hello"}},
			want: provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ResponseID: "completion", ContentID: "text", Text: "hello"},
		},
		{
			name: "barge in",
			data: map[string]any{"textOutput": map[string]any{"completionId": "completion", "contentId": "notice", "content": `{ "interrupted" : true }`}},
			want: provider.RealtimeEvent{Type: provider.RealtimeEventInterrupted, ResponseID: "completion", ContentID: "notice"},
		},
		{
			name: "audio",
			data: map[string]any{"audioOutput": map[string]any{"completionId": "completion", "contentId": "audio", "content": base64.StdEncoding.EncodeToString(audio)}},
			want: provider.RealtimeEvent{Type: provider.RealtimeEventAudioDelta, ResponseID: "completion", ContentID: "audio", Audio: audio},
		},
		{
			name: "tool",
			data: map[string]any{"toolUse": map[string]any{"completionId": "completion", "contentId": "tool", "toolUseId": "call", "toolName": "get_weather", "content": `{"location":"Zurich"}`}},
			want: provider.RealtimeEvent{Type: provider.RealtimeEventToolCall, ResponseID: "completion", ContentID: "tool", ToolCall: &provider.ToolCall{ID: "call", Name: "get_weather", Arguments: `{"location":"Zurich"}`}},
		},
		{
			name: "content end",
			data: map[string]any{"contentEnd": map[string]any{"completionId": "completion", "contentId": "audio", "type": "AUDIO", "stopReason": "END_TURN"}},
			want: provider.RealtimeEvent{Type: provider.RealtimeEventContentDone, ResponseID: "completion", ContentID: "audio", ContentType: provider.RealtimeContentAudio, StopReason: "END_TURN"},
		},
		{
			name: "completion end",
			data: map[string]any{"completionEnd": map[string]any{"completionId": "completion", "stopReason": "END_TURN"}},
			want: provider.RealtimeEvent{Type: provider.RealtimeEventResponseDone, ResponseID: "completion", StopReason: "END_TURN"},
		},
		{
			name: "usage delta",
			data: map[string]any{"usageEvent": map[string]any{
				"completionId": "completion", "totalInputTokens": 101, "totalOutputTokens": 52,
				"details": map[string]any{"delta": map[string]any{
					"input":  map[string]any{"speechTokens": 3, "textTokens": 2},
					"output": map[string]any{"speechTokens": 7, "textTokens": 5},
				}},
			}},
			want: provider.RealtimeEvent{
				Type: provider.RealtimeEventUsage, ResponseID: "completion",
				Usage: &provider.RealtimeUsage{
					InputTokens: 5, OutputTokens: 12,
					InputTextTokens: 2, InputAudioTokens: 3,
					OutputTextTokens: 5, OutputAudioTokens: 7,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(map[string]any{"event": test.data})
			if err != nil {
				t.Fatal(err)
			}
			got, ok, err := parseRealtimeEvent(data)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatal("event was ignored")
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("event = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestNovaOutputTrackerUsesPerTurnUsageDeltas(t *testing.T) {
	tracker := novaOutputTracker{}
	if got := tracker.normalize(provider.RealtimeEvent{
		Type:  provider.RealtimeEventUsage,
		Usage: &provider.RealtimeUsage{InputTokens: 4, InputTextTokens: 4},
	}); len(got) != 0 {
		t.Fatalf("pre-turn usage emitted events: %#v", got)
	}

	started := tracker.normalize(provider.RealtimeEvent{
		Type: provider.RealtimeEventContentStarted, ContentID: "assistant",
		ContentType: provider.RealtimeContentAudio, Role: provider.MessageRoleAssistant,
	})
	if got := eventTypes(started); !reflect.DeepEqual(got, []provider.RealtimeEventType{
		provider.RealtimeEventResponseStarted,
		provider.RealtimeEventUsage,
		provider.RealtimeEventContentStarted,
	}) {
		t.Fatalf("turn start types = %v", got)
	}
	if usage := started[1].Usage; usage == nil || usage.InputTokens != 4 {
		t.Fatalf("initial usage = %#v", usage)
	}

	updated := tracker.normalize(provider.RealtimeEvent{
		Type:  provider.RealtimeEventUsage,
		Usage: &provider.RealtimeUsage{OutputTokens: 3, OutputAudioTokens: 3},
	})
	if len(updated) != 1 || updated[0].Usage == nil {
		t.Fatalf("updated usage events = %#v", updated)
	}
	if got := *updated[0].Usage; got.InputTokens != 4 || got.OutputTokens != 3 || got.OutputAudioTokens != 3 {
		t.Fatalf("accumulated usage = %#v", got)
	}
}

func TestNovaOutputTrackerSynthesizesPerTurnResponseLifecycle(t *testing.T) {
	tracker := novaOutputTracker{}

	if got := tracker.normalize(provider.RealtimeEvent{
		Type: provider.RealtimeEventResponseStarted, ResponseID: "sonic-session-completion",
	}); len(got) != 0 {
		t.Fatalf("session completionStart emitted %v; want no per-turn event", got)
	}

	started := tracker.normalize(provider.RealtimeEvent{
		Type: provider.RealtimeEventContentStarted, ResponseID: "sonic-session-completion",
		ContentID: "user", ContentType: provider.RealtimeContentText,
		Role: provider.MessageRoleUser, Stage: provider.RealtimeGenerationFinal,
	})
	if got := eventTypes(started); !reflect.DeepEqual(got, []provider.RealtimeEventType{
		provider.RealtimeEventResponseStarted, provider.RealtimeEventContentStarted,
	}) {
		t.Fatalf("turn start types = %v", got)
	}
	responseID := started[0].ResponseID
	if !strings.HasPrefix(responseID, "resp_nova_") || started[1].ResponseID != responseID {
		t.Fatalf("turn response IDs = %q, %q", responseID, started[1].ResponseID)
	}

	// Nova can produce multiple speculative sentence chunks followed by the
	// same number of final-transcript chunks. The response is complete only
	// after the counts match.
	for index := 1; index <= 2; index++ {
		contentID := "spec-" + string(rune('0'+index))
		tracker.normalize(provider.RealtimeEvent{
			Type: provider.RealtimeEventContentStarted, ContentID: contentID,
			ContentType: provider.RealtimeContentText, Role: provider.MessageRoleAssistant,
			Stage: provider.RealtimeGenerationSpeculative,
		})
		tracker.normalize(provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ContentID: contentID, Text: "preview"})
		ended := tracker.normalize(provider.RealtimeEvent{Type: provider.RealtimeEventContentDone, ContentID: contentID, ContentType: provider.RealtimeContentText})
		if slices.Contains(eventTypes(ended), provider.RealtimeEventResponseDone) {
			t.Fatalf("speculative content %d completed the response", index)
		}
	}

	for index := 1; index <= 2; index++ {
		contentID := "final-" + string(rune('0'+index))
		tracker.normalize(provider.RealtimeEvent{
			Type: provider.RealtimeEventContentStarted, ContentID: contentID,
			ContentType: provider.RealtimeContentText, Role: provider.MessageRoleAssistant,
			Stage: provider.RealtimeGenerationFinal,
		})
		tracker.normalize(provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ContentID: contentID, Text: "final"})
		ended := tracker.normalize(provider.RealtimeEvent{
			Type: provider.RealtimeEventContentDone, ContentID: contentID,
			ContentType: provider.RealtimeContentText, StopReason: "END_TURN",
		})
		if index == 1 && slices.Contains(eventTypes(ended), provider.RealtimeEventResponseDone) {
			t.Fatal("first final content completed a two-part response")
		}
		if index == 2 {
			if got := eventTypes(ended); !reflect.DeepEqual(got, []provider.RealtimeEventType{
				provider.RealtimeEventContentDone, provider.RealtimeEventResponseDone,
			}) {
				t.Fatalf("last final content types = %v", got)
			}
			if ended[1].ResponseID != responseID {
				t.Fatalf("response.done ID = %q, want %q", ended[1].ResponseID, responseID)
			}
		}
	}

	next := tracker.normalize(provider.RealtimeEvent{
		Type: provider.RealtimeEventContentStarted, ContentID: "next-user",
		ContentType: provider.RealtimeContentText, Role: provider.MessageRoleUser,
	})
	if len(next) != 2 || next[0].ResponseID == responseID {
		t.Fatalf("next turn did not receive a unique response ID: %#v", next)
	}
}

func TestNovaOutputTrackerCompletesToolCallResponse(t *testing.T) {
	tracker := novaOutputTracker{}
	started := tracker.normalize(provider.RealtimeEvent{
		Type: provider.RealtimeEventContentStarted, ContentID: "tool",
		ContentType: provider.RealtimeContentTool, Role: provider.MessageRole("tool"),
	})
	responseID := started[0].ResponseID

	call := tracker.normalize(provider.RealtimeEvent{
		Type: provider.RealtimeEventToolCall, ContentID: "tool",
		ToolCall: &provider.ToolCall{ID: "call", Name: "get_weather", Arguments: `{}`},
	})
	if len(call) != 1 || call[0].ResponseID != responseID {
		t.Fatalf("tool call events = %#v", call)
	}

	ended := tracker.normalize(provider.RealtimeEvent{
		Type: provider.RealtimeEventContentDone, ContentID: "tool",
		ContentType: provider.RealtimeContentTool, StopReason: "TOOL_USE",
	})
	if got := eventTypes(ended); !reflect.DeepEqual(got, []provider.RealtimeEventType{
		provider.RealtimeEventContentDone, provider.RealtimeEventResponseDone,
	}) {
		t.Fatalf("tool completion types = %v", got)
	}
	if ended[1].ResponseID != responseID {
		t.Fatalf("tool response.done ID = %q, want %q", ended[1].ResponseID, responseID)
	}
}

func eventTypes(events []provider.RealtimeEvent) []provider.RealtimeEventType {
	result := make([]provider.RealtimeEventType, 0, len(events))
	for _, event := range events {
		result = append(result, event.Type)
	}
	return result
}
