package harness

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	configuredMu     sync.Mutex
	configuredModels = map[string]map[string]bool{}
)

// ConfiguredModels fetches the model list from a wingman endpoint once per
// base URL and caches it. Returns nil if the listing is unavailable.
func ConfiguredModels(baseURL, apiKey string) map[string]bool {
	configuredMu.Lock()
	defer configuredMu.Unlock()

	if models, ok := configuredModels[baseURL]; ok {
		return models
	}

	configuredModels[baseURL] = nil

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil
	}

	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}

	models := map[string]bool{}

	for _, m := range payload.Data {
		models[m.ID] = true
	}

	for _, m := range payload.Models {
		models[strings.TrimPrefix(m.Name, "models/")] = true
	}

	if len(models) == 0 {
		return nil
	}

	configuredModels[baseURL] = models
	return models
}

// RequireConfigured runs a comparison only against a reachable wingman
// endpoint. A model it doesn't advertise fails, so stale defaults surface
// before the test spends tokens on the reference provider.
func RequireConfigured(t *testing.T, baseURL, apiKey, model string) {
	t.Helper()

	switch unavailable, missing := checkConfigured(baseURL, apiKey, model); {
	case unavailable:
		t.Skip("wingman model listing unavailable — start the backend before running comparison tests")
	case missing:
		t.Fatalf("model %q is not configured in wingman at %s; configure it or choose served models with the suite's TEST_*_MODELS variable", model, baseURL)
	}
}

func checkConfigured(baseURL, apiKey, model string) (unavailable, missing bool) {
	models := ConfiguredModels(baseURL, apiKey)
	return models == nil, models != nil && !models[model]
}
