package anthropic

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestToolResultsNotFollowedByEmptyAssistant(t *testing.T) {
	for _, content := range [][]provider.Content{
		nil,
		{provider.TextContent(" \n\t")},
		{provider.ReasoningContent(provider.Reasoning{Summary: provider.TextParts("Earlier reasoning from another model")})},
	} {
		c, err := NewCompleter("https://bedrock-mantle.eu-north-1.api.aws/anthropic", "anthropic.claude-sonnet-5-5", WithToken("test"))
		if err != nil {
			t.Fatal(err)
		}
		messages := []provider.Message{
			provider.UserMessage("Check the result."),
			{Role: provider.MessageRoleAssistant, Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "check", Arguments: "{}"}),
			}},
			{Role: provider.MessageRoleUser, Content: []provider.Content{
				provider.ToolResultContent(provider.ToolResult{ID: "call_1", Parts: []provider.Part{{Text: "OK"}}}),
			}},
			{Role: provider.MessageRoleAssistant, Content: content},
		}
		body := requestBody(t, c, messages, nil)
		wire := body["messages"].([]any)
		if len(wire) != 3 {
			t.Fatalf("empty assistant turn was sent after tool results: %v", wire)
		}
		last := wire[len(wire)-1].(map[string]any)
		if last["role"] != "user" || last["content"].([]any)[0].(map[string]any)["type"] != "tool_result" {
			t.Fatalf("lost final tool result: %v", last)
		}
	}
}

func TestAssistantContinuationDoesNotReplaceNativeFlows(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   []provider.Message
		options *provider.CompleteOptions
	}{
		{"pending client tool", []provider.Message{provider.UserMessage("start"), {Role: provider.MessageRoleAssistant, Content: []provider.Content{
			provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "check", Arguments: "{}"}),
		}}}, nil},
		{"pending server tool", []provider.Message{provider.UserMessage("start"), {Role: provider.MessageRoleAssistant, Content: []provider.Content{
			provider.ToolCallContent(provider.ToolCall{ID: "srv_1", Kind: provider.ToolKindToolSearch, Name: "tool_search_tool_regex", Arguments: "{}", Execution: "server"}),
		}}}, nil},
		{"compaction trigger", []provider.Message{provider.UserMessage("start"), provider.AssistantMessage("partial")}, &provider.CompleteOptions{CompactionOptions: &provider.CompactionOptions{Trigger: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"invalid_request_error","message":"This model does not support assistant message prefill. The conversation must end with a user message."}}`)), Request: r}, nil
			})}
			c, err := NewCompleter("https://anthropic.test", "claude-sonnet-5-5", WithClient(client), WithMaxRetries(0))
			if err != nil {
				t.Fatal(err)
			}
			var gotErr error
			for _, err := range c.Complete(t.Context(), tc.input, tc.options) {
				gotErr = err
			}
			if gotErr == nil || calls != 1 {
				t.Fatalf("native flow was rewritten: calls=%d err=%v", calls, gotErr)
			}
		})
	}
}

func TestContinuationReplayAcrossMultipleRequests(t *testing.T) {
	boundaries := []assistantContinuation{{Message: 1, Blocks: 1}, {Message: 3, Blocks: 2}}
	for _, redacted := range []bool{false, true} {
		signature := wrapContinuationSignature("SIG", boundaries)
		messages := []provider.Message{
			provider.UserMessage("start"),
			provider.AssistantMessage("first response"),
			provider.AssistantMessage("second response"),
			{Role: provider.MessageRoleAssistant, Content: []provider.Content{
				provider.TextContent("third response"),
				provider.ReasoningContent(provider.Reasoning{Signature: signature, Content: provider.TextParts("thought"), Redacted: redacted}),
				provider.TextContent("fourth response"),
			}},
			provider.UserMessage("follow up"),
		}
		before, _ := json.Marshal(messages)
		c, _ := NewCompleter("https://anthropic.test", "claude-sonnet-5-5")
		body := requestBody(t, c, messages, nil)
		wire := body["messages"].([]any)
		var roles []string
		for _, message := range wire {
			roles = append(roles, message.(map[string]any)["role"].(string))
		}
		if !reflect.DeepEqual(roles, []string{"user", "assistant", "user", "assistant", "user", "assistant", "user"}) {
			t.Fatalf("continuation boundaries lost: %v", wire)
		}
		block := wire[5].(map[string]any)["content"].([]any)[0].(map[string]any)
		field := "signature"
		if redacted {
			field = "data"
		}
		if block[field] != "SIG" {
			t.Fatalf("opaque wrapper leaked to Claude: %v", block)
		}
		after, _ := json.Marshal(messages)
		if string(before) != string(after) {
			t.Fatal("continuation replay modified client history")
		}
	}
}

func TestAssistantContinuationRecoveryIsBounded(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		calls := 0
		client := &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if accepted {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(lifecycleEvents("pause_turn", true))), Request: r}, nil
			}
			return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"This model does not support assistant message prefill."}}`)), Request: r}, nil
		})}
		c, err := NewCompleter("https://anthropic.test", "claude-sonnet-5-5", WithClient(client), WithMaxRetries(0))
		if err != nil {
			t.Fatal(err)
		}
		var gotErr error
		for _, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("start"), provider.AssistantMessage("partial")}, nil) {
			gotErr = err
		}
		if accepted && (gotErr != nil || calls != 1) || !accepted && (gotErr == nil || calls != 2) {
			t.Fatalf("accepted=%t calls=%d err=%v", accepted, calls, gotErr)
		}
	}
}
