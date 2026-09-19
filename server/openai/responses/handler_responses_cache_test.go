package responses

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

func TestResponsesMapPromptCacheFields(t *testing.T) {
	for _, tc := range []struct {
		body string
		want *provider.CacheOptions
	}{
		{`{"model":"test","input":"hi","prompt_cache_key":"session-1","prompt_cache_retention":"24h"}`, &provider.CacheOptions{Key: "session-1", Retention: provider.CacheRetentionExtended}},
		{`{"model":"test","input":"hi","prompt_cache_key":"session-1","prompt_cache_retention":"in_memory"}`, &provider.CacheOptions{Key: "session-1"}},
		{`{"model":"test","input":"hi"}`, nil},
	} {
		completer := &optionsCompleter{}
		cfg := &config.Config{Policy: noop.New()}
		cfg.RegisterCompleter("test", completer)
		rec := httptest.NewRecorder()
		New(cfg).handleResponses(rec, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(tc.body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
		}
		got := completer.options.CacheOptions
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s: cache options = %+v, want %+v", tc.body, got, tc.want)
		}
		if tc.want != nil && !strings.Contains(rec.Body.String(), `"prompt_cache_key":"session-1"`) {
			t.Errorf("%s: response does not echo prompt_cache_key: %s", tc.body, rec.Body.String())
		}
	}
}

// Explicit caching maps to the provider's cache mode, and each marked part
// becomes a breakpoint on the converted content.
func TestResponsesMapExplicitCacheBreakpoints(t *testing.T) {
	completer := &optionsCompleter{}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter("test", completer)
	rec := httptest.NewRecorder()
	body := `{"model":"test","prompt_cache_options":{"mode":"explicit","ttl":"30m"},"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"prefix","prompt_cache_breakpoint":{"mode":"explicit"}},{"type":"input_text","text":"question"}]}]}`
	New(cfg).handleResponses(rec, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(body)))
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
	if !strings.Contains(rec.Body.String(), `"prompt_cache_options":{"mode":"explicit","ttl":"30m"}`) {
		t.Errorf("response does not echo prompt_cache_options: %s", rec.Body.String())
	}
}
