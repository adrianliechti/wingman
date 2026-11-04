package rerank

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"

	"github.com/joho/godotenv"
)

const query = "What is the capital of France?"

var documents = []string{
	"Berlin is the capital of Germany.",
	"Paris is the capital of France.",
	"The Eiffel Tower is in Paris.",
}

func TestRerankEmbedder(t *testing.T) {
	testRerank(t, env("TEST_RERANK_EMBEDDER_MODEL", "text-embedding-3-small"))
}

func TestRerankCompleter(t *testing.T) {
	testRerank(t, env("TEST_RERANK_COMPLETER_MODEL", "gpt-6-luna"))
}

func testRerank(t *testing.T, model string) {
	loadDotenv()

	ep := harness.Endpoint{
		Name:    "wingman",
		BaseURL: env("WINGMAN_BASE_URL", "http://localhost:4242/v1"),
		APIKey:  env("WINGMAN_API_KEY", "test-key"),
	}

	if harness.ConfiguredModels(ep.BaseURL, ep.APIKey) == nil {
		t.Skip("wingman not reachable — start it with `task server`")
	}

	harness.SkipUnlessConfigured(t, ep.BaseURL, ep.APIKey, model)

	results := rerank(t, ep, model, nil)

	if len(results) != len(documents) {
		t.Fatalf("expected %d results, got %d", len(documents), len(results))
	}

	if got := results[0]["text"]; got != documents[1] {
		t.Errorf("top result = %v, want %q", got, documents[1])
	}

	for i := 1; i < len(results); i++ {
		prev, _ := results[i-1]["score"].(float64)
		curr, _ := results[i]["score"].(float64)

		if curr > prev {
			t.Errorf("results[%d].score = %f exceeds previous score %f", i, curr, prev)
		}
	}

	if results = rerank(t, ep, model, 2); len(results) != 2 {
		t.Fatalf("expected 2 results with limit, got %d", len(results))
	}
}

func rerank(t *testing.T, ep harness.Endpoint, model string, limit any) []map[string]any {
	t.Helper()

	body := map[string]any{
		"model": model,
		"query": query,
		"texts": documents,
	}

	if limit != nil {
		body["limit"] = limit
	}

	resp, err := harness.NewClient().Post(context.Background(), ep, "/rerank", body)
	if err != nil {
		t.Fatalf("rerank request failed: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Fatalf("wingman returned status %d: %s", resp.StatusCode, string(resp.RawBody))
	}

	data, ok := resp.Body["results"].([]any)
	if !ok {
		t.Fatalf("results is not an array: %v", resp.Body)
	}

	results := make([]map[string]any, len(data))

	for i, item := range data {
		obj, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("results[%d] is not an object", i)
		}

		results[i] = obj
	}

	return results
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func loadDotenv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		path := filepath.Join(dir, ".env")
		if _, err := os.Stat(path); err == nil {
			_ = godotenv.Load(path)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}
