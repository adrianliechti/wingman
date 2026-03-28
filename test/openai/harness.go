package openaitest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"

	"github.com/joho/godotenv"
)

const (
	DefaultWingmanURL = "http://localhost:8080/v1"
	DefaultOpenAIURL  = "https://api.openai.com/v1"
)

// Harness holds the two endpoints and a shared HTTP client for comparing
// wingman responses against the OpenAI API.
type Harness struct {
	Wingman harness.Endpoint
	OpenAI  harness.Endpoint
	Client  *harness.Client
}

// New creates a Harness from environment variables.
//
//	WINGMAN_BASE_URL  (default http://localhost:8080/v1)
//	WINGMAN_API_KEY   (default "test-key")
//	OPENAI_BASE_URL   (default https://api.openai.com/v1)
//	OPENAI_API_KEY    (required)
func New(t *testing.T) *Harness {
	t.Helper()

	loadDotenv()

	openaiKey := os.Getenv("OPENAI_API_KEY")
	if openaiKey == "" {
		t.Skip("OPENAI_API_KEY not set — skipping comparison tests")
	}

	wingmanURL := envOr("WINGMAN_BASE_URL", DefaultWingmanURL)
	wingmanKey := envOr("WINGMAN_API_KEY", "test-key")
	openaiURL := envOr("OPENAI_BASE_URL", DefaultOpenAIURL)

	return &Harness{
		Wingman: harness.Endpoint{Name: "wingman", BaseURL: wingmanURL, APIKey: wingmanKey},
		OpenAI:  harness.Endpoint{Name: "openai", BaseURL: openaiURL, APIKey: openaiKey},
		Client:  harness.NewClient(),
	}
}

func envOr(key, fallback string) string {
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
