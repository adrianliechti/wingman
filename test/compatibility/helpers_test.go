package compatibility_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	backend "github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/server"
	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

func live(t *testing.T) {
	t.Helper()
	if os.Getenv("API_COMPAT_LIVE") != "1" {
		t.Skip("set API_COMPAT_LIVE=1 to compare with the paid reference APIs")
	}
}

func localServer(t *testing.T, cfg *config.Config) harness.Endpoint {
	t.Helper()
	s, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(s)
	t.Cleanup(local.Close)
	return harness.Endpoint{Name: "wingman", BaseURL: local.URL + "/v1"}
}

func openAI(t *testing.T, model string, decisions bool) *openai.Harness {
	t.Helper()
	live(t)
	h := openai.New(t)
	h.Client.Timeout = 120 * time.Second
	cfg := &config.Config{Policy: noop.New()}
	options := []backend.Option{backend.WithToken(h.OpenAI.APIKey), backend.WithClient(h.Client.HTTP), backend.WithMaxRetries(0)}
	if decisions {
		p, err := backend.NewDecider(h.OpenAI.BaseURL, model, options...)
		if err != nil {
			t.Fatal(err)
		}
		cfg.RegisterDecider(model, p)
	} else {
		p, err := backend.NewResponder(h.OpenAI.BaseURL, model, options...)
		if err != nil {
			t.Fatal(err)
		}
		cfg.RegisterCompleter(model, signatures.ScopedTo(model, p))
	}
	h.Wingman = localServer(t, cfg)
	return h
}

func post(t *testing.T, client *harness.Client, ep harness.Endpoint, path string, body map[string]any, headers http.Header) *harness.RawResponse {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), client.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ep.APIKey)
	req.Header.Set("x-api-key", ep.APIKey)
	resp, err := client.HTTP.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", ep.Name, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	result := &harness.RawResponse{StatusCode: resp.StatusCode, Headers: resp.Header, RawBody: raw}
	if err := json.Unmarshal(raw, &result.Body); err != nil {
		t.Fatalf("%s returned non-JSON (HTTP %d): %.500s", ep.Name, resp.StatusCode, raw)
	}
	return result
}

func requireStatus(t *testing.T, label string, response *harness.RawResponse, want int) {
	t.Helper()
	if response.StatusCode != want {
		t.Fatalf("%s: HTTP %d, want %d: %.1500s", label, response.StatusCode, want, response.RawBody)
	}
}

func field(body map[string]any, path ...string) any {
	var value any = body
	for _, key := range path {
		object, _ := value.(map[string]any)
		value = object[key]
	}
	return value
}

func response(t *testing.T, client *harness.Client, ep harness.Endpoint, body map[string]any, stream bool) map[string]any {
	t.Helper()
	if !stream {
		result := post(t, client, ep, "/responses", body, nil)
		requireStatus(t, ep.Name, result, 200)
		return result.Body
	}
	body = maps.Clone(body)
	body["stream"] = true
	events, err := client.PostSSE(t.Context(), ep, "/responses", body)
	if err != nil {
		t.Fatal(err)
	}
	var terminal map[string]any
	for _, event := range events {
		switch event.Data["type"] {
		case "error", "response.failed":
			t.Fatalf("%s stream failed: %v", ep.Name, event.Data)
		case "response.completed", "response.incomplete":
			if terminal != nil {
				t.Fatalf("%s emitted more than one terminal event", ep.Name)
			}
			terminal, _ = event.Data["response"].(map[string]any)
		}
	}
	if terminal == nil {
		t.Fatalf("%s emitted no terminal response", ep.Name)
	}
	return terminal
}
