package anthropic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"

	"github.com/joho/godotenv"
)

const (
	DefaultWingmanURL       = "http://localhost:8080/v1"
	DefaultAnthropicURL     = "https://api.anthropic.com/v1"
	DefaultAnthropicVersion = "2023-06-01"
)

type Harness struct {
	Wingman   harness.Endpoint
	Anthropic harness.Endpoint
	Client    *harness.Client

	ReferenceModel string
}

func New(t *testing.T) *Harness {
	t.Helper()
	loadDotenv()

	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY not set — skipping comparison tests")
	}

	return &Harness{
		Wingman:        harness.Endpoint{Name: "wingman", BaseURL: env("WINGMAN_BASE_URL", DefaultWingmanURL), APIKey: env("WINGMAN_API_KEY", "test-key")},
		Anthropic:      harness.Endpoint{Name: "anthropic", BaseURL: env("ANTHROPIC_BASE_URL", DefaultAnthropicURL), APIKey: key},
		Client:         harness.NewClient(),
		ReferenceModel: env("TEST_ANTHROPIC_REFERENCE_MODEL", "claude-sonnet-4-6"),
	}
}

type Model struct {
	Name         string
	Capabilities harness.Capabilities
}

func ModelCapabilities(name string) harness.Capabilities {
	n := strings.ToLower(name)

	switch {
	case strings.Contains(n, "bedrock"):
		return harness.Capabilities{Thinking: true, StructuredOutput: true}

	case strings.Contains(n, "claude"):
		switch {
		case strings.Contains(n, "claude-3"):
			return harness.Capabilities{Thinking: true, StructuredOutput: true, Cache: true}
		case strings.Contains(n, "haiku-4-5"):
			return harness.Capabilities{StructuredOutput: true, Cache: true}
		case strings.Contains(n, "-4-0"), strings.Contains(n, "opus-4-1"), strings.Contains(n, "-4-5"):
			return harness.Capabilities{Thinking: true, StructuredOutput: true, Cache: true, TextEditor: true, ComputerUse: true, Shell: true, ToolSearch: true}
		default:
			return harness.Capabilities{Thinking: true, StructuredOutput: true, Cache: true, TextEditor: true, ComputerUse: true, Shell: true, ToolSearch: true, Compaction: true}
		}

	case strings.Contains(n, "gemini"):
		return harness.Capabilities{StructuredOutput: true, Audio: true}

	case strings.HasPrefix(n, "gpt-5.5"):
		// text editor and bash run emulated; tool search uses the hosted tool
		return harness.Capabilities{StructuredOutput: true, Cache: true, TextEditor: true, Shell: true, ToolSearch: true}

	case strings.HasPrefix(n, "gpt-5"):
		// text editor and bash run emulated as function tools
		return harness.Capabilities{StructuredOutput: true, Cache: true, TextEditor: true, Shell: true}

	case strings.HasPrefix(n, "gpt"), strings.HasPrefix(n, "o3"), strings.HasPrefix(n, "o4"):
		return harness.Capabilities{StructuredOutput: true, Cache: true}
	}

	return harness.Capabilities{StructuredOutput: true}
}

func DefaultModels() []Model {
	names := []string{"claude-sonnet-4-6", "bedrock-sonnet-4-6", "gpt-5.4", "gemini-3.5-flash"}
	if v := os.Getenv("TEST_ANTHROPIC_MODELS"); v != "" {
		names = names[:0]
		for s := range strings.SplitSeq(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				names = append(names, s)
			}
		}
	}

	models := make([]Model, len(names))
	for i, name := range names {
		models[i] = Model{Name: name, Capabilities: ModelCapabilities(name)}
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
