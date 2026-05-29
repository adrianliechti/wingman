package gemini

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"

	"github.com/joho/godotenv"
)

const (
	DefaultWingmanURL = "http://localhost:8080/v1beta"
	DefaultGeminiURL  = "https://generativelanguage.googleapis.com/v1beta"
)

type ModelCapabilities struct {
	StructuredOutput bool
	Thinking         bool
}

type Model struct {
	Name         string
	Capabilities ModelCapabilities
}

type Harness struct {
	Wingman        harness.Endpoint
	Gemini         harness.Endpoint
	Client         *harness.Client
	ReferenceModel string
}

func New(t *testing.T) *Harness {
	t.Helper()
	loadDotenv()

	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set — skipping comparison tests")
	}

	return &Harness{
		Wingman:        harness.Endpoint{Name: "wingman", BaseURL: env("WINGMAN_BASE_URL", DefaultWingmanURL), APIKey: env("WINGMAN_API_KEY", "test-key")},
		Gemini:         harness.Endpoint{Name: "gemini", BaseURL: env("GEMINI_BASE_URL", DefaultGeminiURL), APIKey: key},
		Client:         harness.NewClient(),
		ReferenceModel: env("TEST_GEMINI_REFERENCE_MODEL", "gemini-3.5-flash"),
	}
}

func DefaultModels() []Model {
	names := []string{"gemini-3.5-flash"}
	if v := os.Getenv("TEST_GEMINI_MODELS"); v != "" {
		names = names[:0]
		for s := range strings.SplitSeq(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				names = append(names, s)
			}
		}
	}

	models := make([]Model, len(names))
	for i, name := range names {
		models[i] = Model{
			Name:         name,
			Capabilities: ModelCapabilities{StructuredOutput: true, Thinking: true},
		}
	}
	return models
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
