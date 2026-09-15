package bedrock

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

type lifecycleTransport func(*http.Request) (*http.Response, error)

func (f lifecycleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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
			var body bytes.Buffer
			encoder := eventstream.NewEncoder()
			emit := func(kind, payload string) {
				t.Helper()
				if err := encoder.Encode(&body, eventstream.Message{
					Headers: eventstream.Headers{
						{Name: ":message-type", Value: eventstream.StringValue("event")},
						{Name: ":event-type", Value: eventstream.StringValue(kind)},
						{Name: ":content-type", Value: eventstream.StringValue("application/json")},
					},
					Payload: []byte(payload),
				}); err != nil {
					t.Fatal(err)
				}
			}
			emit("messageStart", `{"role":"assistant"}`)
			emit("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"hello"}}`)
			emit("contentBlockStop", `{"contentBlockIndex":0}`)
			if !tc.omitStop {
				emit("messageStop", fmt.Sprintf(`{"stopReason":%q}`, tc.reason))
			}
			emit("metadata", `{"usage":{"inputTokens":2,"outputTokens":3,"totalTokens":5},"metrics":{"latencyMs":1}}`)

			client := &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}}, Body: io.NopCloser(bytes.NewReader(body.Bytes())), Request: r}, nil
			})}
			c := &Completer{
				Config: &Config{model: "anthropic.claude-test"},
				client: bedrockruntime.New(bedrockruntime.Options{
					Region:       "us-east-1",
					Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
					HTTPClient:   client,
					BaseEndpoint: aws.String("http://bedrock.test"),
				}),
			}
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
