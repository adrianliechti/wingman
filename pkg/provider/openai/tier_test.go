package openai

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestTierRequestAndResponse(t *testing.T) {
	for _, chat := range []bool{false, true} {
		for _, test := range []struct {
			tier provider.Tier
			wire string
		}{
			{"", ""}, {"auto", ""}, {provider.TierStandard, "default"}, {"default", ""},
			{provider.TierFlex, "flex"}, {provider.TierPriority, "priority"}, {"ultrafast", ""}, {"unknown", ""},
		} {
			tier := test.tier
			t.Run(fmt.Sprintf("chat=%v/%s", chat, tier), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.UnmarshalRead(r.Body, &body); err != nil {
						t.Error(err)
						return
					}
					want := test.wire
					if want == "" {
						if _, exists := body["service_tier"]; exists {
							t.Error("default request must omit service_tier")
						}
					} else if body["service_tier"] != want {
						t.Errorf("request tier = %v, want %s", body["service_tier"], want)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if chat {
						fmt.Fprint(w, "data: {\"id\":\"c1\",\"service_tier\":\"priority\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n")
						fmt.Fprint(w, "data: {\"id\":\"c1\",\"service_tier\":\"unknown\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
						// Native Mistral usage metadata is retained by the generic adapter.
						fmt.Fprint(w, "data: {\"id\":\"c1\",\"choices\":[],\"usage\":{\"service_tier\":\"standard\",\"prompt_tokens\":1,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n")
					} else {
						fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"service_tier\":\"priority\"}}\n\n")
						fmt.Fprint(w, "event: response.in_progress\ndata: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"r1\",\"service_tier\":\"unknown\"}}\n\n")
						fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"service_tier\":\"default\"}}\n\n")
					}
				}))
				defer server.Close()
				var c provider.Completer
				var err error
				if chat {
					c, err = NewCompleter(server.URL, "test", WithToken("test"))
				} else {
					c, err = NewResponder(server.URL, "test", WithToken("test"))
				}
				if err != nil {
					t.Fatal(err)
				}
				var acc provider.CompletionAccumulator
				for delta, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("hi")}, &provider.CompleteOptions{Tier: tier}) {
					if err != nil {
						t.Fatal(err)
					}
					switch delta.Tier {
					case "", provider.TierStandard, provider.TierFlex, provider.TierPriority:
					default:
						t.Errorf("native tier leaked into completion: %q", delta.Tier)
					}
					acc.Add(*delta)
				}
				if got := acc.Result().Tier; got != provider.TierStandard {
					t.Fatalf("effective tier = %q, want standard", got)
				}
			})
		}
	}
}

func TestUnsupportedTierFallsBackOnce(t *testing.T) {
	for _, chat := range []bool{false, true} {
		for _, rejectedParam := range []string{"service_tier", "model"} {
			t.Run(fmt.Sprintf("chat=%v/%s", chat, rejectedParam), func(t *testing.T) {
				requests := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					var body map[string]any
					if err := json.UnmarshalRead(r.Body, &body); err != nil {
						t.Error(err)
						return
					}
					if requests == 1 {
						if body["service_tier"] != "flex" {
							t.Errorf("missing flex preference: %v", body)
						}
					} else if _, exists := body["service_tier"]; exists {
						t.Error("fallback kept rejected tier")
					}
					// Even a repeated rejection must not cause a retry loop.
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprintf(w, `{"error":{"message":"Unsupported value","type":"invalid_request_error","param":%q}}`, rejectedParam)
				}))
				defer server.Close()
				var c provider.Completer
				var err error
				if chat {
					c, err = NewCompleter(server.URL, "test", WithToken("test"), WithMaxRetries(0))
				} else {
					c, err = NewResponder(server.URL, "test", WithToken("test"), WithMaxRetries(0))
				}
				if err != nil {
					t.Fatal(err)
				}
				options := &provider.CompleteOptions{Tier: provider.TierFlex}
				var runErr error
				for _, err := range c.Complete(t.Context(), nil, options) {
					runErr = err
				}
				want := 1
				if rejectedParam == "service_tier" {
					want = 2
				}
				if runErr == nil || requests != want {
					t.Fatalf("requests=%d, want %d; error=%v", requests, want, runErr)
				}
				if options.Tier != provider.TierFlex {
					t.Fatal("fallback mutated caller options")
				}
			})
		}
	}
}
