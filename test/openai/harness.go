package openai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"

	"github.com/joho/godotenv"
)

const (
	DefaultWingmanURL = "http://localhost:8080/v1"
	DefaultOpenAIURL  = "https://api.openai.com/v1"
)

// ModelCapabilities describes what features a model supports.
type ModelCapabilities struct {
	Reasoning  bool // model supports reasoning/thinking
	Compaction bool // model supports server-side compaction
	TextEditor bool // model supports apply_patch / text_editor tool
}

// Model represents a model to test with its provider context.
type Model struct {
	Name         string
	Capabilities ModelCapabilities
}

// DefaultModels returns the list of models to test.
// Override with TEST_MODELS env var (comma-separated; prefix with + for OpenAI models).
func DefaultModels() []Model {
	if v := os.Getenv("TEST_MODELS"); v != "" {
		var models []Model
		for _, m := range strings.Split(v, ",") {
			m = strings.TrimSpace(m)
			models = append(models, Model{Name: strings.TrimSpace(m)})
		}
		return models
	}

	return []Model{
		{Name: "gpt-5.4-mini", Capabilities: ModelCapabilities{Reasoning: true, Compaction: true, TextEditor: true}},
		{Name: "claude-sonnet-4-6", Capabilities: ModelCapabilities{Reasoning: true, TextEditor: true}},
		{Name: "bedrock-sonnet-4-6", Capabilities: ModelCapabilities{Reasoning: true}},
	}
}

// Harness holds the two endpoints and a shared HTTP client for comparing
// wingman responses against the OpenAI API.
type Harness struct {
	Wingman harness.Endpoint
	OpenAI  harness.Endpoint
	Client  *harness.Client

	// ReferenceModel is the model used for OpenAI API reference calls
	// when testing non-OpenAI models through wingman.
	ReferenceModel string
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
		Wingman:        harness.Endpoint{Name: "wingman", BaseURL: wingmanURL, APIKey: wingmanKey},
		OpenAI:         harness.Endpoint{Name: "openai", BaseURL: openaiURL, APIKey: openaiKey},
		Client:         harness.NewClient(),
		ReferenceModel: envOr("TEST_REFERENCE_MODEL", "gpt-5.4-mini"),
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
