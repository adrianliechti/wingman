package responses

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

type tierCompleter struct {
	options  *provider.CompleteOptions
	reported provider.Tier
}

func (c *tierCompleter) Complete(_ context.Context, _ []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	c.options = options
	return func(yield func(*provider.Completion, error) bool) {
		if !yield(&provider.Completion{Tier: c.reported}, nil) {
			return
		}
		if !yield(&provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.TextContent("ok")}}}, nil) {
			return
		}
		// An explicit downgrade must replace an earlier tier; missing metadata
		// in intervening deltas must not erase it.
		if c.reported != "" {
			yield(&provider.Completion{Tier: provider.TierStandard}, nil)
		}
	}
}

func TestResponsesTier(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, test := range []struct {
			input string
			want  provider.Tier
		}{
			{"", ""}, {`,"service_tier":null`, ""},
			{`,"service_tier":"auto"`, ""},
			{`,"service_tier":"default"`, provider.TierStandard},
			{`,"service_tier":"standard"`, provider.TierStandard},
			{`,"service_tier":"flex"`, provider.TierFlex},
			{`,"service_tier":"priority"`, provider.TierPriority},
			{`,"service_tier":"unknown"`, ""},
			{`,"service_tier":"fast"`, provider.TierPriority},
			{`,"service_tier":"ultrafast"`, ""},
		} {
			t.Run(fmt.Sprintf("stream=%v/%s", stream, test.input), func(t *testing.T) {
				c := &tierCompleter{reported: provider.TierPriority}
				cfg := &config.Config{Policy: noop.New()}
				cfg.RegisterCompleter("test", c)
				body := fmt.Sprintf(`{"model":"test","input":"hi","stream":%v%s}`, stream, test.input)
				rec := httptest.NewRecorder()
				New(cfg).handleResponses(rec, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(body)))
				if rec.Code != http.StatusOK {
					t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
				}
				if c.options.Tier != test.want {
					t.Fatalf("tier = %q, want %q", c.options.Tier, test.want)
				}
				var response map[string]any
				if stream {
					events, err := harness.ParseSSE(rec.Body)
					if err != nil {
						t.Fatal(err)
					}
					for _, event := range events {
						if value, ok := event.Data["response"].(map[string]any); ok {
							response = value
							if response["status"] == "in_progress" && response["service_tier"] != nil {
								t.Fatal("invented effective tier before provider response")
							}
						}
					}
				} else if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if response["service_tier"] != "default" {
					t.Fatalf("effective tier = %#v, want downgrade to default", response["service_tier"])
				}
			})
		}
	}
}

func TestResponseTierUnavailable(t *testing.T) {
	for _, completion := range []*provider.Completion{nil, {}, {Tier: "unknown"}, {Tier: "ultrafast"}} {
		resp := &Response{}
		responseDefaults(resp, ResponsesRequest{Tier: "flex"}, completion)
		if resp.Tier != nil {
			t.Fatalf("invented tier: %v", *resp.Tier)
		}
	}
}
