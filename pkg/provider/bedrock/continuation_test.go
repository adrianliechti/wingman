package bedrock

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

const prefillRejection = `{"message":"The model returned the following errors: This model does not support assistant message prefill. The conversation must end with a user message."}`

// continuationCompleter rejects every request that ends with an assistant
// turn, as current Claude models do, and records what it was sent.
func continuationCompleter(t *testing.T, acceptContinuation bool, events ...[2]string) (*Completer, *[][]map[string]any) {
	t.Helper()
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")

	if len(events) == 0 {
		events = [][2]string{
			{"messageStart", `{"role":"assistant"}`},
			{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"4\n5\n6"}}`},
			{"contentBlockStop", `{"contentBlockIndex":0}`},
			{"messageStop", `{"stopReason":"end_turn"}`},
			{"metadata", `{"usage":{"inputTokens":10,"outputTokens":5,"totalTokens":15}}`},
		}
	}
	var stream bytes.Buffer
	encoder := eventstream.NewEncoder()
	for _, event := range events {
		if err := encoder.Encode(&stream, eventstream.Message{
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

	requests := &[][]map[string]any{}
	client := &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		*requests = append(*requests, body.Messages)
		last := body.Messages[len(body.Messages)-1]
		if last["role"] == "assistant" || !acceptContinuation {
			return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}, "X-Amzn-Errortype": {"ValidationException"}}, Body: io.NopCloser(strings.NewReader(prefillRejection)), Request: r}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}}, Body: io.NopCloser(bytes.NewReader(stream.Bytes())), Request: r}, nil
	})}
	return &Completer{
		Config: &Config{model: "anthropic.claude-test"},
		client: bedrockruntime.New(bedrockruntime.Options{
			Region:           "us-east-1",
			Credentials:      credentials.NewStaticCredentialsProvider("test", "test", ""),
			HTTPClient:       client,
			BaseEndpoint:     aws.String("http://bedrock.test"),
			RetryMaxAttempts: 1,
		}),
	}, requests
}

func complete(t *testing.T, c *Completer, messages []provider.Message) (string, error) {
	t.Helper()
	var text strings.Builder
	var failure error
	for delta, err := range c.Complete(t.Context(), messages, nil) {
		if err != nil {
			failure = err
			continue
		}
		if delta != nil && delta.Message != nil {
			text.WriteString(delta.Message.Text())
		}
	}
	return text.String(), failure
}

func TestConversationEndingWithAssistantTurnIsContinued(t *testing.T) {
	c, requests := continuationCompleter(t, true)
	text, err := complete(t, c, []provider.Message{provider.UserMessage("Count to six."), provider.AssistantMessage("1\n2\n3")})
	if err != nil || text != "4\n5\n6" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	if len(*requests) != 2 {
		t.Fatalf("requests=%d, want the native attempt and one continuation", len(*requests))
	}
	first, second := (*requests)[0], (*requests)[1]
	if len(first) != 2 || first[1]["role"] != "assistant" {
		t.Fatalf("native attempt was rewritten: %v", first)
	}
	if len(second) != 3 || second[1]["role"] != "assistant" || second[2]["role"] != "user" {
		t.Fatalf("continuation changed the client's turns: %v", second)
	}
	added, _ := json.Marshal(second[2]["content"])
	if !strings.Contains(string(added), provider.ContinuationText) {
		t.Fatalf("continuation turn=%s", added)
	}
}

func TestAssistantContinuationIsBoundedAndKeepsNativeFlows(t *testing.T) {
	pendingTool := []provider.Message{provider.UserMessage("start"), {Role: provider.MessageRoleAssistant, Content: []provider.Content{
		provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "check", Arguments: "{}"}),
	}}}
	for _, tc := range []struct {
		name     string
		messages []provider.Message
		calls    int
	}{
		{"continuation rejected too", []provider.Message{provider.UserMessage("start"), provider.AssistantMessage("partial")}, 2},
		{"pending tool call", pendingTool, 1},
	} {
		c, requests := continuationCompleter(t, false)
		_, err := complete(t, c, tc.messages)
		if err == nil || !strings.Contains(err.Error(), "assistant message prefill") || len(*requests) != tc.calls {
			t.Errorf("%s: calls=%d err=%v, want %d calls and the upstream error", tc.name, len(*requests), err, tc.calls)
		}
	}
}

// A signature produced after the inserted turn must find that turn again on every later request.
func TestContinuationBoundarySurvivesLaterRequests(t *testing.T) {
	c, requests := continuationCompleter(t, true,
		[2]string{"messageStart", `{"role":"assistant"}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":"thought"}}}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"signature":"SIG"}}}`},
		[2]string{"contentBlockStop", `{"contentBlockIndex":0}`},
		[2]string{"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"text":"4\n5\n6"}}`},
		[2]string{"contentBlockStop", `{"contentBlockIndex":1}`},
		[2]string{"messageStop", `{"stopReason":"end_turn"}`},
		[2]string{"metadata", `{"usage":{"inputTokens":10,"outputTokens":5,"totalTokens":15}}`},
	)
	history := []provider.Message{provider.UserMessage("Count to six."), provider.AssistantMessage("1\n2\n3")}
	var acc provider.CompletionAccumulator
	for delta, err := range c.Complete(t.Context(), history, nil) {
		if err != nil {
			t.Fatal(err)
		}
		acc.Add(*delta)
	}
	continued := acc.Result().Message
	var signature string
	for _, content := range continued.Content {
		if content.Reasoning != nil && content.Reasoning.Signature != "" {
			signature = content.Reasoning.Signature
		}
	}
	raw, boundaries := provider.UnwrapContinuationSignature(signature)
	if raw != "SIG" || len(boundaries) != 1 || boundaries[0] != (provider.Continuation{Message: 1, Blocks: 1}) {
		t.Fatalf("signature=%q carries %v, want SIG with the boundary after the partial answer", signature, boundaries)
	}

	*requests = nil
	followUp := append(append(history, *continued), provider.UserMessage("And the next two?"))
	before, _ := json.Marshal(followUp)
	if _, err := complete(t, c, followUp); err != nil {
		t.Fatal(err)
	}
	if len(*requests) != 1 {
		t.Fatalf("requests=%d, want one", len(*requests))
	}
	wire := (*requests)[0]
	var roles []string
	for _, message := range wire {
		roles = append(roles, message["role"].(string))
	}
	if strings.Join(roles, ",") != "user,assistant,user,assistant,user" {
		t.Fatalf("inserted turn was not replayed: %v", wire)
	}
	inserted, _ := json.Marshal(wire[2]["content"])
	resumed, _ := json.Marshal(wire[3]["content"])
	if !strings.Contains(string(inserted), provider.ContinuationText) {
		t.Fatalf("replayed turn=%s", inserted)
	}
	if !strings.Contains(string(resumed), `"signature":"SIG"`) || strings.Contains(string(resumed), "wm:claude-continuation") {
		t.Fatalf("opaque wrapper reached Bedrock: %s", resumed)
	}
	if after, _ := json.Marshal(followUp); string(after) != string(before) {
		t.Fatal("replay modified the client's history")
	}
}
