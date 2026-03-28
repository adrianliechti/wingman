package gemini

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"

	"github.com/joho/godotenv"
)

const (
	DefaultWingmanURL = "http://localhost:8080/v1beta"
	DefaultGeminiURL  = "https://generativelanguage.googleapis.com/v1beta"
)

// ModelCapabilities describes what features a model supports.
type ModelCapabilities struct {
	StructuredOutput bool
	Thinking         bool
}

// Model represents a model to test with its provider context.
type Model struct {
	Name         string
	Capabilities ModelCapabilities
}

// Harness holds the two endpoints and a shared HTTP client for comparing
// wingman responses against the Gemini API.
type Harness struct {
	Wingman        harness.Endpoint
	Gemini         harness.Endpoint
	Client         *harness.Client
	ReferenceModel string
}

// New creates a Harness from environment variables.
func New(t *testing.T) *Harness {
	t.Helper()

	loadDotenv()

	geminiKey := os.Getenv("GEMINI_API_KEY")
	if geminiKey == "" {
		t.Skip("GEMINI_API_KEY not set — skipping comparison tests")
	}

	wingmanURL := envOr("WINGMAN_BASE_URL", DefaultWingmanURL)
	wingmanKey := envOr("WINGMAN_API_KEY", "test-key")
	geminiURL := envOr("GEMINI_BASE_URL", DefaultGeminiURL)

	return &Harness{
		Wingman:        harness.Endpoint{Name: "wingman", BaseURL: wingmanURL, APIKey: wingmanKey},
		Gemini:         harness.Endpoint{Name: "gemini", BaseURL: geminiURL, APIKey: geminiKey},
		Client:         harness.NewClient(),
		ReferenceModel: envOr("TEST_GEMINI_REFERENCE_MODEL", "gemini-3-flash-preview"),
	}
}

// DefaultModels returns the list of models to test.
func DefaultModels() []Model {
	return []Model{
		{Name: "gemini-3-flash-preview", Capabilities: ModelCapabilities{StructuredOutput: true, Thinking: true}},
		{Name: "gpt-5.4-mini", Capabilities: ModelCapabilities{StructuredOutput: true, Thinking: true}},
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
