package claudecode

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestClaudeCode(t *testing.T) {
	if os.Getenv("CLAUDE_CODE_LIVE") != "1" {
		t.Skip("set CLAUDE_CODE_LIVE=1 to run paid Claude Code comparison tests")
	}
	h := anthropic.New(t)
	binary := os.Getenv("CLAUDE_CODE_BIN")
	if binary == "" {
		binary = "claude"
	}
	binary, err := exec.LookPath(binary)
	if err != nil {
		t.Fatalf("Claude Code is required: %v", err)
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	version, err := exec.Command(binary, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Claude Code version: %s", version)
	models := anthropic.DefaultModels()
	if os.Getenv("TEST_ANTHROPIC_MODELS") == "" {
		// Opus makes the CLI exercise mid-conversation system messages.
		models = append(models, anthropic.Model{Name: "claude-opus-5-5"})
	} else if configured := harness.ConfiguredModels(h.Wingman.BaseURL, h.Wingman.APIKey); configured != nil {
		for _, model := range models {
			if !configured[model.Name] {
				t.Fatalf("explicitly requested model %q is not configured in Wingman", model.Name)
			}
		}
	}
	input := fmt.Sprintf("fixture-%s\n", rand.Text())
	for _, scenario := range []claudeScenario{
		{
			name:   "text",
			prompt: "Reply with exactly WINGMAN_E2E_OK and nothing else.",
			want:   "WINGMAN_E2E_OK",
		},
		{
			name:   "read_edit",
			prompt: "Use Read to read input.txt and output.txt. Then use Edit to replace REPLACE_ME in output.txt with the text from input.txt, excluding input.txt's trailing newline. Preserve output.txt's existing final newline so both files contain exactly the same bytes. Do not use other tools. Reply with DONE after editing the file.",
			tools:  "Read,Edit",
			input:  input,
			want:   input,
		},
		projectScenario(t),
	} {
		t.Run(scenario.name, func(t *testing.T) {
			run := func(t *testing.T, endpoint harness.Endpoint, model string) outcome {
				answer, exchanges, fixture := runClaudeWithOptions(t, binary, endpoint, model, scenario.prompt, scenario.tools, scenario.input, scenario.options)
				tools := checkExchanges(t, exchanges, model)
				var wantTools []string
				if scenario.tools != "" {
					wantTools = strings.Split(scenario.tools, ",")
					slices.Sort(wantTools)
				}
				if !slices.Equal(tools, wantTools) {
					t.Errorf("expected completed tools %v, got %v", wantTools, tools)
				}
				if scenario.input != "" {
					data, err := os.ReadFile(filepath.Join(fixture, "output.txt"))
					if err != nil {
						t.Fatalf("Claude Code did not write output.txt: %v", err)
					}
					answer = string(data)
				}
				if scenario.verify != nil {
					scenario.verify(t, exchanges, fixture)
				}
				if answer != scenario.want {
					t.Errorf("outcome %q, want %q", answer, scenario.want)
				}
				return outcome{answer, tools}
			}
			var reference outcome
			referenceRan := false
			if !t.Run("anthropic", func(t *testing.T) {
				reference = run(t, h.Anthropic, h.ReferenceModel)
				referenceRan = true
			}) {
				t.Fatal("Anthropic reference failed; cannot compare Wingman")
			}
			for _, model := range models {
				t.Run("wingman/"+model.Name, func(t *testing.T) {
					h.SkipUnlessConfigured(t, model.Name)
					actual := run(t, h.Wingman, model.Name)
					if referenceRan && !reflect.DeepEqual(actual, reference) {
						t.Errorf("outcome differs from Anthropic: got %+v, want %+v", actual, reference)
					}
				})
			}
		})
	}
}

type claudeScenario struct {
	name, prompt, tools, input, want string
	options                          claudeRunOptions
	verify                           func(*testing.T, []exchange, string)
}

// Compare task results and completed tool names, not generated prose, IDs,
// token counts, number of requests, or SSE chunk boundaries.
type outcome struct {
	Answer string
	Tools  []string
}
