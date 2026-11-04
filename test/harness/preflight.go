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

// SkipUnlessConfigured requires a reachable wingman endpoint advertising the
// model before a comparison test spends tokens on the reference provider.
func SkipUnlessConfigured(t *testing.T, baseURL, apiKey, model string) {
	t.Helper()

	models := ConfiguredModels(baseURL, apiKey)

	if models == nil {
		t.Skip("wingman model listing unavailable — start the backend before running comparison tests")
	}

	if !models[model] {
		t.Skipf("model %q not configured in wingman", model)
	}
}
