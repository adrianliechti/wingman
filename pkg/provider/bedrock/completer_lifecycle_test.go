package bedrock

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

type lifecycleTransport func(*http.Request) (*http.Response, error)

func (f lifecycleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// scriptedCompleter serves the given ConverseStream events, each an event type
// and its JSON payload.
func scriptedCompleter(t *testing.T, events ...[2]string) *Completer {
	t.Helper()
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")

	var body bytes.Buffer
	encoder := eventstream.NewEncoder()
	for _, event := range events {
		if err := encoder.Encode(&body, eventstream.Message{
			Headers: eventstream.Headers{
				{Name: ":message-type", Value: eventstream.StringValue("event")},
				{Name: ":event-type", Value: eventstream.StringValue(event[0])},
				{Name: ":content-type", Value: eventstream.StringValue("application/json")},
			},
			Payload: []byte(event[1]),
		}); err != nil {
			t.Fatal(err)
		}
	}

	client := &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}}, Body: io.NopCloser(bytes.NewReader(body.Bytes())), Request: r}, nil
	})}

	return &Completer{
		Config: &Config{model: "anthropic.claude-test"},
		client: bedrockruntime.New(bedrockruntime.Options{
			Region:       "us-east-1",
			Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
			HTTPClient:   client,
			BaseEndpoint: aws.String("http://bedrock.test"),
		}),
	}
}

// TestCompleterTextItemsFollowBlocks verifies text splits into message items
// like the Anthropic adapter's: adjacent text blocks share an item, and text
// after a reasoning block starts the next one, so replaying the items keeps
// interleaved thinking in place.
func TestCompleterTextItemsFollowBlocks(t *testing.T) {
	c := scriptedCompleter(t,
		[2]string{"messageStart", `{"role":"assistant"}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":"plan"}}}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"signature":"SIG1"}}}`},
		[2]string{"contentBlockStop", `{"contentBlockIndex":0}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"text":"First"}}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"text":" update."}}`},
		[2]string{"contentBlockStop", `{"contentBlockIndex":1}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":2,"delta":{"reasoningContent":{"text":"check"}}}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":2,"delta":{"reasoningContent":{"signature":"SIG2"}}}`},
		[2]string{"contentBlockStop", `{"contentBlockIndex":2}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":3,"delta":{"text":"Second"}}`},
		[2]string{"contentBlockStop", `{"contentBlockIndex":3}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":4,"delta":{"text":" update."}}`},
		[2]string{"contentBlockStop", `{"contentBlockIndex":4}`},
		[2]string{"contentBlockStart", `{"contentBlockIndex":5,"start":{"toolUse":{"toolUseId":"tool_1","name":"lookup"}}}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":5,"delta":{"toolUse":{"input":"{}"}}}`},
		[2]string{"contentBlockStop", `{"contentBlockIndex":5}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":6,"delta":{"text":"After the call."}}`},
		[2]string{"contentBlockStop", `{"contentBlockIndex":6}`},
		[2]string{"messageStop", `{"stopReason":"tool_use"}`},
		[2]string{"metadata", `{"usage":{"inputTokens":2,"outputTokens":3,"totalTokens":5},"metrics":{"latencyMs":1}}`},
	)

	var acc provider.CompletionAccumulator
	for delta, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("hi")}, nil) {
		if err != nil {
			t.Fatal(err)
		}
		acc.Add(*delta)
	}

	var got []string
	for _, message := range acc.Result().Message.SplitMessages() {
		var parts []string
		for _, part := range message.Content {
			switch {
			case part.Reasoning != nil:
				parts = append(parts, "reasoning:"+part.Reasoning.Text())
			case part.ToolCall != nil:
				parts = append(parts, "call:"+part.ToolCall.ID)
			case part.Text != "":
				parts = append(parts, "text:"+part.Text)
			}
		}
		got = append(got, strings.Join(parts, ", "))
	}

	// Reasoning stays with the item it followed; the flattened order is what
	// a client replays.
	want := []string{"reasoning:plan, text:First update., reasoning:check", "text:Second update., call:tool_1", "text:After the call."}
	if !slices.Equal(got, want) {
		t.Fatalf("items = %q, want %q", got, want)
	}
}

func TestCompleterSchemaTextKeepsOrder(t *testing.T) {
	c := scriptedCompleter(t,
		[2]string{"messageStart", `{"role":"assistant"}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"Let me check."}}`},
		[2]string{"contentBlockStop", `{"contentBlockIndex":0}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"reasoningContent":{"text":"plan"}}}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"reasoningContent":{"signature":"SIG"}}}`},
		[2]string{"contentBlockStop", `{"contentBlockIndex":1}`},
		[2]string{"contentBlockStart", `{"contentBlockIndex":2,"start":{"toolUse":{"toolUseId":"schema_1","name":"answer"}}}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":2,"delta":{"toolUse":{"input":"{\"ok\":true}"}}}`},
		[2]string{"contentBlockStop", `{"contentBlockIndex":2}`},
		[2]string{"messageStop", `{"stopReason":"tool_use"}`},
	)
	c.model = "anthropic.claude-opus-5-5"

	var acc provider.CompletionAccumulator
	options := &provider.CompleteOptions{Schema: &provider.Schema{Name: "answer"}}
	for delta, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("hi")}, options) {
		if err != nil {
			t.Fatal(err)
		}
		acc.Add(*delta)
	}

	result := acc.Result()
	parts := result.Message.Content
	if len(parts) != 3 || parts[0].Text != "Let me check." || parts[1].Reasoning == nil || parts[2].Text != `{"ok":true}` {
		t.Fatalf("schema answer moved across reasoning: %+v", parts)
	}
	if result.StopReason != provider.StopReasonEndTurn {
		t.Fatalf("schema tool surfaced as a tool call: %q", result.StopReason)
	}
}

func TestCompleterNativeStopReasons(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		omitStop     bool
		wantError    bool
		wantReason   provider.StopReason
		wantStatus   provider.CompletionStatus
	}{
		{name: "end", reason: "end_turn", wantReason: provider.StopReasonEndTurn},
		{name: "unknown", reason: "future_reason", wantReason: "future_reason"},
		{name: "max tokens", reason: "max_tokens", wantReason: provider.StopReasonMaxTokens, wantStatus: provider.CompletionStatusIncomplete},
		{name: "context exceeded", reason: "model_context_window_exceeded", wantReason: provider.StopReasonContextExceeded, wantStatus: provider.CompletionStatusIncomplete},
		{name: "refusal", reason: "guardrail_intervened", wantReason: provider.StopReasonRefusal, wantStatus: provider.CompletionStatusRefused},
		{name: "malformed tool use", reason: "malformed_tool_use", wantError: true},
		{name: "malformed output", reason: "malformed_model_output", wantError: true},
		{name: "missing reason", wantError: true},
		{name: "missing stop", omitStop: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := [][2]string{
				{"messageStart", `{"role":"assistant"}`},
				{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"hello"}}`},
				{"contentBlockStop", `{"contentBlockIndex":0}`},
			}
			if !tc.omitStop {
				events = append(events, [2]string{"messageStop", fmt.Sprintf(`{"stopReason":%q}`, tc.reason)})
			}
			events = append(events, [2]string{"metadata", `{"usage":{"inputTokens":2,"outputTokens":3,"totalTokens":5},"metrics":{"latencyMs":1}}`})

			c := scriptedCompleter(t, events...)
			var acc provider.CompletionAccumulator
			var runErr error
			for delta, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("hi")}, nil) {
				runErr = errors.Join(runErr, err)
				if delta != nil {
					acc.Add(*delta)
				}
			}
			if (runErr != nil) != tc.wantError {
				t.Fatalf("error = %v, wantError = %t", runErr, tc.wantError)
			}
			if tc.omitStop && !errors.Is(runErr, io.ErrUnexpectedEOF) {
				t.Fatalf("missing stop error = %v, want unexpected EOF", runErr)
			}
			if tc.wantError {
				return
			}
			result := acc.Result()
			if result.StopReason != tc.wantReason || result.Status != tc.wantStatus || result.Text() != "hello" {
				t.Fatalf("completion = %+v, text = %q", result, result.Text())
			}
			if result.Usage == nil || result.Usage.InputTokens != 2 || result.Usage.OutputTokens != 3 {
				t.Fatalf("lost metadata after messageStop: %+v", result.Usage)
			}
		})
	}
}
