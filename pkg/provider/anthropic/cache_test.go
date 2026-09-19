package anthropic

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/anthropics/anthropic-sdk-go"
)

// The stable prefix is cached automatically on every request; extended
// retention lengthens the TTL and nothing else changes.
func TestPrefixCacheIsDefaultAndRetentionExtendsIt(t *testing.T) {
	c, err := NewCompleter("http://anthropic.test", "claude-sonnet-4-6")
	if err != nil {
		t.Fatal(err)
	}
	messages := []provider.Message{provider.UserMessage("hi")}

	req, err := c.convertMessageRequest(messages, &provider.CompleteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if req.CacheControl.TTL != "" {
		t.Fatalf("default TTL = %q, want the backend default", req.CacheControl.TTL)
	}

	req, err = c.convertMessageRequest(messages, &provider.CompleteOptions{CacheOptions: &provider.CacheOptions{Retention: provider.CacheRetentionExtended}})
	if err != nil {
		t.Fatal(err)
	}
	if req.CacheControl.TTL != anthropic.BetaCacheControlEphemeralTTLTTL1h {
		t.Fatalf("extended TTL = %q, want 1h", req.CacheControl.TTL)
	}
}

// Explicit mode drops the automatic prefix cache and marks exactly the
// client's breakpoints, each with its own retention; implicit mode does the
// opposite.
func TestExplicitCacheModeMarksBreakpoints(t *testing.T) {
	c, err := NewCompleter("http://anthropic.test", "claude-sonnet-4-6")
	if err != nil {
		t.Fatal(err)
	}
	messages := []provider.Message{
		{Role: provider.MessageRoleSystem, Content: []provider.Content{{Text: "rules", CacheControl: &provider.CacheControl{Retention: provider.CacheRetentionExtended}}}},
		provider.UserMessage("hi"),
		{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "f", Arguments: "{}"})}},
		{Role: provider.MessageRoleUser, Content: []provider.Content{{ToolResult: &provider.ToolResult{ID: "call_1", Parts: []provider.Part{{Text: "ok"}}}, CacheControl: &provider.CacheControl{}}}},
	}

	explicit := requestBody(t, c, messages, &provider.CompleteOptions{CacheOptions: &provider.CacheOptions{Mode: provider.CacheModeExplicit}})
	if explicit["cache_control"] != nil {
		t.Errorf("explicit mode must not cache the whole prefix: %v", explicit["cache_control"])
	}
	system := explicit["system"].([]any)[0].(map[string]any)
	if control, _ := system["cache_control"].(map[string]any); control["type"] != "ephemeral" || control["ttl"] != "1h" {
		t.Errorf("system breakpoint = %v, want an ephemeral 1h breakpoint", system["cache_control"])
	}
	result := explicit["messages"].([]any)[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if control, _ := result["cache_control"].(map[string]any); control["type"] != "ephemeral" || control["ttl"] != nil {
		t.Errorf("tool result breakpoint = %v, want a default-retention breakpoint", result["cache_control"])
	}
	question := explicit["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if question["cache_control"] != nil {
		t.Errorf("unmarked part got a breakpoint: %v", question)
	}

	implicit := requestBody(t, c, messages, &provider.CompleteOptions{})
	if implicit["cache_control"] == nil {
		t.Error("implicit mode must cache the whole prefix")
	}
	if system := implicit["system"].([]any)[0].(map[string]any); system["cache_control"] != nil {
		t.Errorf("implicit mode must leave block breakpoints to the automatic cache: %v", system)
	}
}
