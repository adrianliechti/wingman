package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/anthropics/anthropic-sdk-go"
)

func TestCompactionTriggerAndReplay(t *testing.T) {
	c, _ := NewCompleter("http://anthropic.test", "claude-sonnet-4-6")
	options := &provider.CompleteOptions{CompactionOptions: &provider.CompactionOptions{Trigger: true}}
	body := requestBody(t, c, []provider.Message{provider.UserMessage("remember ALPHA-7")}, options)
	if body["compaction"].(map[string]any)["type"] != "summarize" || body["context_management"] != nil {
		t.Fatalf("incorrect on-demand request: %+v", body)
	}
	block := compactionFromBlock("msg_1", 0, "ALPHA-7", "", `{"signature":"signed-summary"}`)
	for _, role := range []provider.MessageRole{provider.MessageRoleAssistant, provider.MessageRoleUser} {
		messages := []provider.Message{
			provider.SystemMessage("Keep secrets."),
			{Role: role, Content: []provider.Content{provider.CompactionContent(block)}},
			provider.UserMessage("Recall the code."),
		}
		req, err := c.convertMessageRequest(messages, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(req.Betas) != 1 || req.Betas[0] != "compact-2026-09-04" {
			t.Fatalf("replay beta = %v", req.Betas)
		}
		data, _ := json.Marshal(req.Messages[0].Content[0])
		var replay map[string]any
		_ = json.Unmarshal(data, &replay)
		if replay["signature"] != "signed-summary" || replay["content"] != "ALPHA-7" || replay["encrypted_content"] != nil {
			t.Fatalf("signed block changed: %s", data)
		}
		if _, err := c.convertMessageRequest(messages, &provider.CompleteOptions{CompactionOptions: &provider.CompactionOptions{Threshold: 50000}}); err == nil {
			t.Fatal("signed block accepted with threshold compaction")
		}
	}
	input := []provider.Message{provider.UserMessage("remember ALPHA-7"), {Content: []provider.Content{provider.CompactionTriggerContent()}}}
	body = requestBody(t, c, input, &provider.CompleteOptions{})
	if body["compaction"] == nil || len(body["messages"].([]any)) != 1 {
		t.Fatalf("trigger item was lost or replayed as a message: %+v", body)
	}
	if _, err := c.convertMessageRequest(append(input, provider.UserMessage("later")), nil); err == nil {
		t.Fatal("a trigger in the middle of the input must not compact later messages")
	}
}

func TestCompactionSignatureRealm(t *testing.T) {
	for _, value := range []string{"raw", "@claude-test:raw"} {
		wrapped := WrapCompactionSignature(value)
		decoded, signed := UnwrapCompactionSignature(wrapped)
		if !signed || decoded != value {
			t.Fatalf("signature changed: %q -> %q", value, decoded)
		}
		if decoded, signed := UnwrapCompactionSignature(value); signed || decoded != value {
			t.Fatal("legacy encrypted payload reinterpreted as a signature")
		}
	}
}

func TestThresholdCompactionReplayKeepsStrategy(t *testing.T) {
	c, _ := NewCompleter("http://anthropic.test", "claude-sonnet-4-6")
	for _, signature := range []string{"", "encrypted-metadata"} {
		messages := []provider.Message{
			{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.CompactionContent(provider.Compaction{Content: "ALPHA-7", Signature: signature})}},
			provider.UserMessage("Recall the code."),
		}
		body := requestBody(t, c, messages, nil)
		cm, ok := body["context_management"].(map[string]any)
		if !ok {
			t.Fatal("threshold replay lost its required context management strategy")
		}
		edit := cm["edits"].([]any)[0].(map[string]any)
		if edit["type"] != "compact_20260112" {
			t.Fatalf("wrong strategy: %v", edit)
		}
	}
}

func TestCompactionUsageIncludesAllIterations(t *testing.T) {
	usage := toUsage(anthropic.BetaUsage{
		InputTokens: 20, OutputTokens: 5,
		Iterations: anthropic.BetaIterationsUsage{
			{Type: "compaction", InputTokens: 50000, OutputTokens: 200, CacheReadInputTokens: 100, CacheCreationInputTokens: 50},
			{Type: "message", InputTokens: 20, OutputTokens: 5},
		},
	})
	if usage.InputTokens != 50170 || usage.OutputTokens != 205 || usage.CacheReadInputTokens != 100 || usage.CacheCreationInputTokens != 50 {
		t.Fatalf("compaction usage lost or double-counted: %+v", usage)
	}
	usage = toUsage(anthropic.BetaUsage{Iterations: anthropic.BetaIterationsUsage{{Type: "compaction", InputTokens: 30, OutputTokens: 15}}})
	if usage == nil || usage.InputTokens != 30 || usage.OutputTokens != 15 {
		t.Fatalf("summary-only usage lost: %+v", usage)
	}
}

func TestCompactionRejectsIncompatibleOptions(t *testing.T) {
	c, _ := NewCompleter("http://anthropic.test", "claude-sonnet-4-6")
	for _, options := range []*provider.CompleteOptions{
		{Stop: []string{"STOP"}},
		{Schema: &provider.Schema{}},
		{ToolOptions: &provider.ToolOptions{Choice: provider.ToolChoiceAny}},
	} {
		options.CompactionOptions = &provider.CompactionOptions{Trigger: true}
		if _, err := c.convertMessageRequest([]provider.Message{provider.UserMessage("hello")}, options); err == nil || !strings.Contains(err.Error(), "compaction") {
			t.Fatalf("expected compaction error, got %v", err)
		}
	}
	c, _ = NewCompleter("http://anthropic.test", "claude-haiku-4-5")
	if _, err := c.convertMessageRequest([]provider.Message{provider.UserMessage("hello")}, &provider.CompleteOptions{CompactionOptions: &provider.CompactionOptions{Trigger: true}}); err == nil {
		t.Fatal("unsupported model silently ignored compaction")
	}
}
