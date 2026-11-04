package anthropic

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestTierMapping(t *testing.T) {
	c := &Completer{Config: &Config{model: "claude-opus-5-5"}}
	for _, test := range []struct {
		tier   provider.Tier
		native string
	}{
		{"", ""}, {"auto", ""}, {provider.TierStandard, "standard"},
		{provider.TierFlex, ""}, {provider.TierPriority, "fast"}, {"unknown", ""},
		{"default", ""}, {"fast", ""}, {"ultrafast", ""},
	} {
		req, err := c.convertMessageRequest([]provider.Message{provider.UserMessage("hi")}, &provider.CompleteOptions{Tier: test.tier})
		if err != nil {
			t.Fatal(err)
		}
		if string(req.Speed) != test.native {
			t.Errorf("%s: native speed = %q, want %q", test.tier, req.Speed, test.native)
		}
		if req.ServiceTier != "" {
			t.Fatal("processing preference must not select legacy capacity tiers")
		}
	}
}

func TestTierFromUsage(t *testing.T) {
	for _, test := range []struct {
		native string
		want   provider.Tier
	}{{"standard", provider.TierStandard}, {"fast", provider.TierPriority}, {"", ""}, {"priority", ""}, {"unknown", ""}} {
		events := lifecycleEvents("end_turn", true)
		events = strings.Replace(events, `"input_tokens":10`, `"speed":"`+test.native+`","service_tier":"standard","input_tokens":10`, 1)
		c, err := NewCompleter("http://anthropic.test", "claude-opus-5-5", WithClient(&http.Client{Transport: lifecycleTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(events))}, nil
		})}))
		if err != nil {
			t.Fatal(err)
		}
		var acc provider.CompletionAccumulator
		for delta, err := range c.Complete(t.Context(), nil, nil) {
			if err != nil {
				t.Fatal(err)
			}
			acc.Add(*delta)
		}
		if got := acc.Result().Tier; got != test.want {
			t.Fatalf("effective tier = %q, want %q", got, test.want)
		}
	}
}

func TestFastTierFallback(t *testing.T) {
	for _, test := range []struct {
		status   int
		message  string
		fallback bool
	}{
		{400, "This model does not support speed fast", true},
		{422, "Unsupported speed value", true},
		{429, "Rate limited", true},
		{529, "Overloaded", true},
		{400, "Invalid model", false},
	} {
		t.Run(fmt.Sprint(test.status)+test.message, func(t *testing.T) {
			requests := 0
			client := &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				var body map[string]any
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
					t.Fatal(err)
				}
				if requests == 1 {
					if body["speed"] != "fast" {
						t.Errorf("missing fast preference: %v", body)
					}
					payload, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": test.message}})
					return &http.Response{StatusCode: test.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(payload)))}, nil
				}
				if body["speed"] != "standard" {
					t.Errorf("fallback did not select standard: %v", body)
				}
				events := strings.Replace(lifecycleEvents("end_turn", true), `"input_tokens":10`, `"speed":"standard","input_tokens":10`, 1)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(events))}, nil
			})}
			c, err := NewCompleter("http://anthropic.test", "claude-test", WithClient(client), WithMaxRetries(0))
			if err != nil {
				t.Fatal(err)
			}
			options := &provider.CompleteOptions{Tier: provider.TierPriority}
			var acc provider.CompletionAccumulator
			var runErr error
			for delta, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("hi")}, options) {
				if err != nil {
					runErr = err
					break
				}
				acc.Add(*delta)
			}
			if test.fallback {
				if runErr != nil || requests != 2 || acc.Result().Tier != provider.TierStandard {
					t.Fatalf("requests=%d, tier=%q, error=%v", requests, acc.Result().Tier, runErr)
				}
			} else if runErr == nil || requests != 1 {
				t.Fatalf("unexpected retry: requests=%d error=%v", requests, runErr)
			}
			if options.Tier != provider.TierPriority {
				t.Fatal("caller options mutated")
			}
		})
	}
}
