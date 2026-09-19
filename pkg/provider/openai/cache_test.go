package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

// OpenAI caches prefixes by default; the key and extended retention are the
// only refinements, and both surfaces forward them.
func TestRequestsCarryCacheOptions(t *testing.T) {
	messages := []provider.Message{provider.UserMessage("hi")}
	extended := &provider.CompleteOptions{CacheOptions: &provider.CacheOptions{Key: "session-1", Retention: provider.CacheRetentionExtended}}

	responder, err := NewResponder("", "gpt-5.4")
	if err != nil {
		t.Fatal(err)
	}
	req, err := responder.convertResponsesRequest(messages, extended)
	if err != nil {
		t.Fatal(err)
	}
	if req.PromptCacheKey.Value != "session-1" || req.PromptCacheRetention != responses.ResponseNewParamsPromptCacheRetention24h {
		t.Fatalf("responses cache fields = %q %q", req.PromptCacheKey.Value, req.PromptCacheRetention)
	}
	req, err = responder.convertResponsesRequest(messages, &provider.CompleteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if req.PromptCacheKey.Valid() || req.PromptCacheRetention != "" {
		t.Fatalf("responses cache fields set without options: %q %q", req.PromptCacheKey.Value, req.PromptCacheRetention)
	}

	completer, err := NewCompleter("", "gpt-5.4")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := completer.convertCompletionRequest(messages, extended)
	if err != nil {
		t.Fatal(err)
	}
	if chat.PromptCacheKey.Value != "session-1" || chat.PromptCacheRetention != openai.ChatCompletionNewParamsPromptCacheRetention24h {
		t.Fatalf("chat cache fields = %q %q", chat.PromptCacheKey.Value, chat.PromptCacheRetention)
	}
}

// Explicit mode and breakpoints reach models that support them; earlier
// models keep implicit caching and receive neither field.
func TestExplicitCacheBreakpointsForwardedToSupportingModels(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.MessageRoleSystem, Content: []provider.Content{{Text: "rules", CacheControl: &provider.CacheControl{}}}},
		provider.UserMessage("hi"),
	}
	options := &provider.CompleteOptions{CacheOptions: &provider.CacheOptions{Mode: provider.CacheModeExplicit}}

	for _, tc := range []struct {
		model string
		want  bool
	}{
		{"gpt-5.6", true},
		{"gpt-6-astra", true},
		{"gpt-5.4", false},
	} {
		responder, err := NewResponder("", tc.model)
		if err != nil {
			t.Fatal(err)
		}
		req, err := responder.convertResponsesRequest(messages, options)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(req)
		mode := strings.Contains(string(data), `"prompt_cache_options":{"mode":"explicit"}`)
		breakpoint := strings.Contains(string(data), `"prompt_cache_breakpoint":{"mode":"explicit"}`)
		if mode != tc.want || breakpoint != tc.want {
			t.Errorf("%s responses: mode=%t breakpoint=%t, want %t: %s", tc.model, mode, breakpoint, tc.want, data)
		}

		completer, err := NewCompleter("", tc.model)
		if err != nil {
			t.Fatal(err)
		}
		chat, err := completer.convertCompletionRequest(messages, options)
		if err != nil {
			t.Fatal(err)
		}
		data, _ = json.Marshal(chat)
		mode = strings.Contains(string(data), `"prompt_cache_options":{"mode":"explicit"}`)
		breakpoint = strings.Contains(string(data), `"prompt_cache_breakpoint":{"mode":"explicit"}`)
		if mode != tc.want || breakpoint != tc.want {
			t.Errorf("%s chat: mode=%t breakpoint=%t, want %t: %s", tc.model, mode, breakpoint, tc.want, data)
		}
	}
}
