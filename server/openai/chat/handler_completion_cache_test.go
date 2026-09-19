package chat

import (
	"context"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
)

// optionsCompleter records the provider options a request was mapped to.
type optionsCompleter struct {
	options  *provider.CompleteOptions
	messages []provider.Message
}

func (c *optionsCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	c.options, c.messages = options, messages
	return func(yield func(*provider.Completion, error) bool) {
		yield(&provider.Completion{
			Message:    &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.TextContent("ok")}},
			StopReason: provider.StopReasonEndTurn,
		}, nil)
	}
}

func TestChatCompletionsMapPromptCacheFields(t *testing.T) {
	completer := &optionsCompleter{}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter("test", completer)
	rec := httptest.NewRecorder()
	body := `{"model":"test","messages":[{"role":"user","content":"hi"}],"prompt_cache_key":"session-1","prompt_cache_retention":"24h"}`
	New(cfg).handleChatCompletion(rec, httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	want := provider.CacheOptions{Key: "session-1", Retention: provider.CacheRetentionExtended}
	if got := completer.options.CacheOptions; got == nil || *got != want {
		t.Fatalf("cache options = %+v, want %+v", got, want)
	}
}

// Explicit caching maps to the provider's cache mode, and each marked part
// becomes a breakpoint on the converted content.
func TestChatCompletionsMapExplicitCacheBreakpoints(t *testing.T) {
	completer := &optionsCompleter{}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter("test", completer)
	rec := httptest.NewRecorder()
	body := `{"model":"test","prompt_cache_options":{"mode":"explicit"},"messages":[{"role":"user","content":[{"type":"text","text":"prefix","prompt_cache_breakpoint":{"mode":"explicit"}},{"type":"text","text":"question"}]}]}`
	New(cfg).handleChatCompletion(rec, httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if got := completer.options.CacheOptions; got == nil || got.Mode != provider.CacheModeExplicit {
		t.Fatalf("cache options = %+v, want explicit mode", got)
	}
	content := completer.messages[len(completer.messages)-1].Content
	if len(content) != 2 || content[0].CacheControl == nil || content[1].CacheControl != nil {
		t.Fatalf("breakpoints = %+v, want one on the first part", content)
	}
}
