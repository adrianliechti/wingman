package chat

import (
	"encoding/json/v2"
	"testing"
)

func TestMessageContentUnionV2(t *testing.T) {
	var message ChatCompletionMessage
	for _, input := range []string{`{"content":""}`, `{"content":[{"type":"text","text":"hi"}]}`, `{"content":null}`} {
		if err := json.Unmarshal([]byte(input), &message); err != nil {
			t.Fatal(err)
		}
		if message.Content != nil && message.Contents != nil {
			t.Fatal("reused message retained both union variants")
		}
	}
	for _, input := range []string{`{"content":42}`, `{"content":{}}`, `{"content":true}`, `{"content":"a","content":"b"}`} {
		if err := json.Unmarshal([]byte(input), &message); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	text := ""
	data, err := json.Marshal(&ChatCompletionMessage{Content: &text})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["content"] != "" {
		t.Fatalf("omitted explicit empty content: %s", data)
	}
	if _, ok := fields["tool_calls"]; ok {
		t.Fatalf("included unset tools: %s", data)
	}
}
