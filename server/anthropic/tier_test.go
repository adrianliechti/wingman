package anthropic

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

type speedCompleter struct {
	options  *provider.CompleteOptions
	reported provider.Tier
}

func (c *speedCompleter) Complete(_ context.Context, _ []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	c.options = options
	return func(yield func(*provider.Completion, error) bool) {
		yield(&provider.Completion{Tier: c.reported, StopReason: provider.StopReasonEndTurn, Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.TextContent("ok")}}}, nil)
	}
}

func TestMessagesSpeed(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, test := range []struct {
			speed string
			want  provider.Tier
		}{{"", ""}, {"standard", provider.TierStandard}, {"fast", provider.TierPriority}, {"unknown", ""}} {
			t.Run(fmt.Sprint(stream)+"/"+test.speed, func(t *testing.T) {
				// Report standard even if fast was requested to test downgrades.
				c := &speedCompleter{reported: provider.TierStandard}
				cfg := &config.Config{Policy: noop.New()}
				cfg.RegisterCompleter("test", c)
				body := fmt.Sprintf(`{"model":"test","messages":[{"role":"user","content":"hi"}],"max_tokens":16,"stream":%v,"speed":%q}`, stream, test.speed)
				rec := httptest.NewRecorder()
				New(cfg).handleMessages(rec, httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(body)))
				if rec.Code != http.StatusOK {
					t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
				}
				if c.options.Tier != test.want {
					t.Fatalf("tier=%q, want %q", c.options.Tier, test.want)
				}
				if stream {
					events, err := harness.ParseSSE(rec.Body)
					if err != nil {
						t.Fatal(err)
					}
					checked := 0
					for _, event := range events {
						data := event.Data
						if message, ok := data["message"].(map[string]any); ok {
							data = message
						}
						if usage, ok := data["usage"].(map[string]any); ok {
							if event.Data["type"] == "message_delta" {
								if _, exists := usage["speed"]; exists {
									t.Fatal("message_delta must not include speed")
								}
								continue
							}
							if usage["speed"] != "standard" {
								t.Fatalf("wrong effective speed: %v", usage)
							}
							checked++
						}
					}
					if checked != 1 {
						t.Fatalf("checked %d speed envelopes, want message_start", checked)
					}
				} else {
					var message Message
					if err := json.Unmarshal(rec.Body.Bytes(), &message); err != nil {
						t.Fatal(err)
					}
					if message.Usage.Speed != "standard" {
						t.Fatalf("effective speed=%q", message.Usage.Speed)
					}
				}
			})
		}
	}
}
