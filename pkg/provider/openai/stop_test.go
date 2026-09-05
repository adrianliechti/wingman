package openai

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func text(s string) *provider.Completion {
	return &provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.TextContent(s)}}}
}

func TestStopFilterCutsAcrossChunks(t *testing.T) {
	f := newStopFilter([]string{"STOP", "6"})

	out, hit := f.filter(text("1, 2, 3, "))
	if hit || out.Message.Content[0].Text != "1, 2, 3, " {
		t.Fatalf("no match yet: %+v %v", out.Message.Content, hit)
	}

	out, hit = f.filter(text("4, 5, 6, 7"))
	if !hit {
		t.Fatal("expected a hit")
	}
	if got := out.Message.Content[0].Text; got != "4, 5, " {
		t.Errorf("truncated text: %q", got)
	}
	if out.StopReason != provider.StopReasonStopSequence || out.StopSequence != "6" {
		t.Errorf("stop state: %q %q", out.StopReason, out.StopSequence)
	}

	if _, hit := f.filter(text("more")); hit {
		t.Error("filter must stay done")
	}
}

func TestStopFilterMatchAtChunkStart(t *testing.T) {
	f := newStopFilter([]string{"END"})

	out, hit := f.filter(text("END of story"))
	if !hit || len(out.Message.Content) != 0 {
		t.Fatalf("expected an empty hit, got %+v", out.Message.Content)
	}
}

func TestSanitizeToolIDs(t *testing.T) {
	long := "call_1::get_weather::" + string(make([]byte, 200))

	messages := sanitizeToolIDs([]provider.Message{
		{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{ID: long, Name: "get_weather"})}},
		provider.ToolMessage(long, "ok"),
		provider.ToolMessage("call_plain", "ok"),
	})

	if id := messages[0].Content[0].ToolCall.ID; id != "call_1" {
		t.Errorf("call id: %q", id)
	}
	if id := messages[1].Content[0].ToolResult.ID; id != "call_1" {
		t.Errorf("result id: %q", id)
	}
	if id := messages[2].Content[0].ToolResult.ID; id != "call_plain" {
		t.Errorf("plain id must pass through: %q", id)
	}
}
