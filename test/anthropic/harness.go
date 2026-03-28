package anthropic

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"

	"github.com/joho/godotenv"
)

const (
	DefaultWingmanURL    = "http://localhost:8080/v1"
	DefaultAnthropicURL  = "https://api.anthropic.com/v1"
	DefaultAnthropicVersion = "2023-06-01"
)

// Harness holds the two endpoints and a shared HTTP client for comparing
// wingman responses against the Anthropic API.
type Harness struct {
	Wingman   harness.Endpoint
	Anthropic harness.Endpoint
	Client    *harness.Client

	ReferenceModel string
}

// New creates a Harness from environment variables.
func New(t *testing.T) *Harness {
	t.Helper()

	loadDotenv()

	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	if anthropicKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set — skipping comparison tests")
	}

	wingmanURL := envOr("WINGMAN_BASE_URL", DefaultWingmanURL)
	wingmanKey := envOr("WINGMAN_API_KEY", "test-key")
	anthropicURL := envOr("ANTHROPIC_BASE_URL", DefaultAnthropicURL)

	return &Harness{
		Wingman:        harness.Endpoint{Name: "wingman", BaseURL: wingmanURL, APIKey: wingmanKey},
		Anthropic:      harness.Endpoint{Name: "anthropic", BaseURL: anthropicURL, APIKey: anthropicKey},
		Client:         harness.NewClient(),
		ReferenceModel: envOr("TEST_ANTHROPIC_REFERENCE_MODEL", "claude-sonnet-4-6"),
	}
}

// ModelCapabilities describes what features a model supports.
type ModelCapabilities struct {
	Thinking   bool
	TextEditor bool
}

// Model represents a model to test with its provider context.
type Model struct {
	Name         string
	Capabilities ModelCapabilities
}

// DefaultModels returns the list of models to test against the Anthropic API.
func DefaultModels() []Model {
	return []Model{
		{Name: "claude-sonnet-4-6", Capabilities: ModelCapabilities{Thinking: true, TextEditor: true}},
		{Name: "bedrock-sonnet-4-6", Capabilities: ModelCapabilities{Thinking: true}},
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
