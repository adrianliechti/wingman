package gemini

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestGenerateThinkingLevel(t *testing.T) {
	for level, want := range map[string]provider.Effort{
		"low": provider.EffortLow, "MEDIUM": provider.EffortMedium, "high": provider.EffortHigh,
		"XHIGH": provider.EffortXHigh, "THINKING_LEVEL_XHIGH": provider.EffortXHigh,
	} {
		c := &tierCompleter{}
		cfg := &config.Config{Policy: noop.New()}
		cfg.RegisterCompleter("test", c)
		body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":"` + level + `"}}}`
		r := httptest.NewRequest(http.MethodPost, "/models/test:generateContent", strings.NewReader(body))
		r.SetPathValue("model", "test")
		rec := httptest.NewRecorder()
		New(cfg).handleGenerateContent(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: HTTP %d: %s", level, rec.Code, rec.Body.String())
		}
		if got := c.options.ReasoningOptions.Effort; got != want {
			t.Errorf("%s: effort = %q, want %q", level, got, want)
		}
	}
}

func TestGenerateContentUnknownModel(t *testing.T) {
	h := New(&config.Config{Policy: noop.New()})

	req := httptest.NewRequest(http.MethodPost, "/models/missing:generateContent", strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
	req.SetPathValue("model", "missing")

	rec := httptest.NewRecorder()
	h.handleGenerateContent(rec, req)

	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"NOT_FOUND"`) {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
}
