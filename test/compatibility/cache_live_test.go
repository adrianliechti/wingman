package compatibility_test

import (
	"encoding/json/jsontext"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	claude "github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestExtendedCacheClaude(t *testing.T) {
	live(t)
	h := anthropic.New(t)
	const model = "claude-sonnet-5-5"
	p, err := claude.NewCompleter(strings.TrimSuffix(h.Anthropic.BaseURL, "/v1"), model, claude.WithToken(h.Anthropic.APIKey), claude.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(model, p)
	h.Wingman = localServer(t, cfg)
	for _, ep := range []harness.Endpoint{h.Anthropic, h.Wingman} {
		prefix := fmt.Sprintf("%s-%d\n", ep.Name, time.Now().UnixNano()) + strings.Repeat("The scene contains a red square on a white canvas. ", 300) + "\nReply only OK."
		var total, standard, extended any
		if ep.Name == h.Anthropic.Name {
			body := map[string]any{"model": model, "max_tokens": 32, "cache_control": map[string]any{"type": "ephemeral", "ttl": "1h"}, "messages": []any{map[string]any{"role": "user", "content": prefix}}}
			r := post(t, h.Client, ep, "/messages", body, http.Header{"Anthropic-Version": {"2026-10-21"}})
			requireStatus(t, ep.Name, r, 200)
			total = field(r.Body, "usage", "cache_creation_input_tokens")
			standard = field(r.Body, "usage", "cache_creation", "ephemeral_5m_input_tokens")
			extended = field(r.Body, "usage", "cache_creation", "ephemeral_1h_input_tokens")
		} else {
			body := responseBody(model, prefix)
			body["prompt_cache_options"] = map[string]any{"ttl": "12h"}
			r := post(t, h.Client, ep, "/responses", body, nil)
			requireStatus(t, ep.Name, r, 200)
			total = field(r.Body, "usage", "input_tokens_details", "cache_write_tokens")
			standard = field(r.Body, "usage", "input_tokens_details", "cache_write_tokens_details", "ttl_30m_tokens")
			extended = field(r.Body, "usage", "input_tokens_details", "cache_write_tokens_details", "ttl_12h_tokens")
		}
		short, _ := standard.(float64)
		long, _ := extended.(float64)
		if long <= 0 || total != short+long {
			t.Fatalf("%s cache writes: total=%v standard=%v extended=%v", ep.Name, total, standard, extended)
		}
	}
}

func TestDecisionsCacheAndApplicationState(t *testing.T) {
	const model = "gpt-6-luna"
	h := openAI(t, model, true)
	d, err := openai.NewDecider(h.OpenAI.BaseURL, model, openai.WithToken(h.OpenAI.APIKey), openai.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	state := `[{"role":"system","uptime":5}]`
	result, err := d.Decide(t.Context(), &provider.DecisionInput{State: jsontext.Value(state), Questions: []provider.DecisionQuestion{{ID: "q", Instructions: "Is uptime five?", Noul: &provider.NoulQuestion{}}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Answers[0].Noul.Probability <= 0.5 {
		t.Fatalf("lost application state: %+v", result.Answers)
	}
	for _, ep := range []harness.Endpoint{h.OpenAI, h.Wingman} {
		key := fmt.Sprintf("decisions-%s-%d", ep.Name, time.Now().UnixNano())
		body := map[string]any{"model": model, "input": key + strings.Repeat("The color is red. ", 400), "questions": []any{map[string]any{"type": "predicate", "name": "q", "instructions": "Is the color red?"}}, "prompt_cache_key": key, "prompt_cache_options": map[string]any{"ttl": "12h"}}
		r := post(t, h.Client, ep, "/decisions", body, nil)
		requireStatus(t, ep.Name, r, 200)
		long, _ := field(r.Body, "usage", "input_tokens_details", "cache_write_tokens_details", "ttl_12h_tokens").(float64)
		if long <= 0 || field(r.Body, "usage", "input_tokens_details", "cache_write_tokens") != long {
			t.Fatalf("%s lost extended cache writes: %v", ep.Name, r.Body["usage"])
		}
	}
}

func TestOlderModelCacheIntent(t *testing.T) {
	const model = "gpt-5.5"
	h := openAI(t, model, false)
	for _, ep := range []harness.Endpoint{h.OpenAI, h.Wingman} {
		body := responseBody(model, "Reply only OK.")
		body["prompt_cache_options"] = map[string]any{"ttl": "7d"}
		requireStatus(t, ep.Name+" invalid TTL", post(t, h.Client, ep, "/responses", body, nil), 400)
		body["prompt_cache_options"] = map[string]any{"ttl": "12h"}
		if ep.Name == h.OpenAI.Name {
			delete(body, "prompt_cache_options")
			body["prompt_cache_retention"] = "24h"
		}
		requireStatus(t, ep.Name+" extended retention", post(t, h.Client, ep, "/responses", body, nil), 200)
	}
}

func TestNativeChatExtendedCache(t *testing.T) {
	const model = "gpt-6.1-sol"
	h := openAI(t, model, false)
	backend, err := openai.NewCompleter(h.OpenAI.BaseURL, model, openai.WithToken(h.OpenAI.APIKey), openai.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	var acc provider.CompletionAccumulator
	for chunk, err := range backend.Complete(t.Context(), []provider.Message{provider.UserMessage("Reply only OK.")}, &provider.CompleteOptions{CacheOptions: &provider.CacheOptions{Retention: provider.CacheRetentionExtended}}) {
		if err != nil {
			t.Fatal(err)
		}
		acc.Add(*chunk)
	}
	if result := acc.Result(); result.Usage == nil || result.Usage.InputTokens == 0 || result.Text() == "" {
		t.Fatalf("incomplete extended-cache response: %+v", result)
	}
}

func TestResponsesFastAlias(t *testing.T) {
	h := openAI(t, "gpt-6.1-sol", false)
	for _, stream := range []bool{false, true} {
		for _, tier := range []string{"fast", "priority"} {
			for _, ep := range []harness.Endpoint{h.OpenAI, h.Wingman} {
				body := responseBody("gpt-6.1-sol", "Reply only OK.")
				body["service_tier"] = tier
				r := response(t, h.Client, ep, body, stream)
				if r["service_tier"] != "fast" && r["service_tier"] != "priority" {
					t.Errorf("%s requested %s, got %v", ep.Name, tier, r["service_tier"])
				}
			}
		}
	}
}
