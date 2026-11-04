package gemini

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/test/harness"
)

type tierCompleter struct{ options *provider.CompleteOptions }

func (c *tierCompleter) Complete(_ context.Context, _ []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	c.options = options
	return func(yield func(*provider.Completion, error) bool) {
		yield(&provider.Completion{Tier: provider.TierStandard, Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.TextContent("ok")}}}, nil)
	}
}

func TestGenerateTier(t *testing.T) {
	for _, mode := range []string{"json", "array", "sse"} {
		for _, field := range []string{"serviceTier", "service_tier"} {
			for _, test := range []struct {
				native string
				want   provider.Tier
			}{
				{"", ""}, {"auto", ""}, {"standard", provider.TierStandard},
				{"flex", provider.TierFlex}, {"priority", provider.TierPriority}, {"unknown", ""},
				{"default", ""}, {"fast", ""}, {"ultrafast", ""},
			} {
				t.Run(mode+"/"+field+"/"+test.native, func(t *testing.T) {
					c := &tierCompleter{}
					cfg := &config.Config{Policy: noop.New()}
					cfg.RegisterCompleter("test", c)
					body := fmt.Sprintf(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],%q:%q}`, field, test.native)
					r := httptest.NewRequest(http.MethodPost, "/models/test:generateContent?alt="+mode, strings.NewReader(body))
					r.SetPathValue("model", "test")
					rec := httptest.NewRecorder()
					if mode == "json" {
						New(cfg).handleGenerateContent(rec, r)
					} else {
						New(cfg).handleStreamGenerateContent(rec, r)
					}
					if rec.Code != http.StatusOK {
						t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
					}
					if c.options.Tier != test.want {
						t.Fatalf("tier = %q, want %q", c.options.Tier, test.want)
					}
					if rec.Header().Get("X-Gemini-Service-Tier") != "standard" {
						t.Fatal("missing effective tier header")
					}
					var response map[string]any
					switch mode {
					case "json":
						if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
							t.Fatal(err)
						}
					case "array":
						var chunks []map[string]any
						if err := json.Unmarshal(rec.Body.Bytes(), &chunks); err != nil {
							t.Fatal(err)
						}
						response = chunks[len(chunks)-1]
					case "sse":
						events, err := harness.ParseSSE(rec.Body)
						if err != nil {
							t.Fatal(err)
						}
						response = events[len(events)-1].Data
					}
					metadata, _ := response["usageMetadata"].(map[string]any)
					if metadata["serviceTier"] != "standard" {
						t.Fatalf("effective tier = %v, want standard", metadata)
					}
				})
			}
		}
	}
}
