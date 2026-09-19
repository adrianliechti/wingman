package anthropic

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

// Cache breakpoints are read as intent: the prefix is cached by default, and
// a 1h TTL anywhere asks for extended retention.
func TestMessagesMapCacheControl(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       *provider.CacheOptions
	}{
		{"system 1h", `{"model":"test","max_tokens":16,"system":[{"type":"text","text":"rules","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":"hi"}]}`, &provider.CacheOptions{Retention: provider.CacheRetentionExtended}},
		{"message 1h", `{"model":"test","max_tokens":16,"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`, &provider.CacheOptions{Retention: provider.CacheRetentionExtended}},
		{"default ttl", `{"model":"test","max_tokens":16,"system":[{"type":"text","text":"rules","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hi"}]}`, nil},
		{"no breakpoints", `{"model":"test","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			completer := &optionsCompleter{}
			cfg := &config.Config{Policy: noop.New()}
			cfg.RegisterCompleter("test", completer)
			rec := httptest.NewRecorder()
			New(cfg).handleMessages(rec, httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(tc.body)))
			if rec.Code != http.StatusOK {
				t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
			}
			got := completer.options.CacheOptions
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("cache options = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Every cache_control breakpoint becomes a marker on the converted part; the
// system prompt's breakpoint lands on the system message, and a 1h TTL is
// carried as that breakpoint's retention.
func TestMessagesMarkCacheBreakpoints(t *testing.T) {
	completer := &optionsCompleter{}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter("test", completer)
	rec := httptest.NewRecorder()
	body := `{"model":"test","max_tokens":16,"system":[{"type":"text","text":"rules","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"prefix","cache_control":{"type":"ephemeral","ttl":"1h"}},{"type":"text","text":"question"}]}]}`
	New(cfg).handleMessages(rec, httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	messages := completer.messages
	if len(messages) != 2 || messages[0].Role != provider.MessageRoleSystem {
		t.Fatalf("messages = %+v", messages)
	}
	if control := messages[0].Content[len(messages[0].Content)-1].CacheControl; control == nil || control.Retention != "" {
		t.Errorf("system breakpoint = %+v, want a default-retention marker", control)
	}
	content := messages[1].Content
	if len(content) != 2 || content[0].CacheControl == nil || content[0].CacheControl.Retention != provider.CacheRetentionExtended || content[1].CacheControl != nil {
		t.Errorf("message breakpoints = %+v, want an extended one on the first part only", content)
	}
	if got := completer.options.CacheOptions; got == nil || got.Retention != provider.CacheRetentionExtended || got.Mode != provider.CacheModeImplicit {
		t.Errorf("cache options = %+v, want extended retention in implicit mode", got)
	}
}
