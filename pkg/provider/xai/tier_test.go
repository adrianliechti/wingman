package xai

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type tierTransport func(*http.Request) (*http.Response, error)

func (f tierTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTierMapping(t *testing.T) {
	for _, tier := range []provider.Tier{"", provider.Tier("auto"), provider.TierStandard, provider.TierFlex, provider.TierPriority, "unknown"} {
		t.Run(string(tier), func(t *testing.T) {
			client := &http.Client{Transport: tierTransport(func(r *http.Request) (*http.Response, error) {
				var body map[string]any
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
					t.Fatal(err)
				}
				if tier == provider.TierStandard || tier == provider.TierPriority {
					want := "priority"
					if tier == provider.TierStandard {
						want = "default"
					}
					if body["service_tier"] != want {
						t.Errorf("tier = %v, want %s", body["service_tier"], want)
					}
				} else if _, exists := body["service_tier"]; exists {
					t.Error("unsupported preference should keep backend default")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"service_tier\":\"default\"}}\n\n"))}, nil
			})}
			c, err := NewCompleter("grok-test", WithToken("test"), WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			options := &provider.CompleteOptions{Tier: tier}
			for _, err := range c.Complete(t.Context(), nil, options) {
				if err != nil {
					t.Fatal(err)
				}
			}
			if options.Tier != tier {
				t.Fatal("caller options mutated")
			}
		})
	}
}
