package claudecode

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	input := fmt.Sprintf("fixture-%s\n", rand.Text())
	for _, scenario := range []struct {
		name, prompt, tools, input, want string
	}{
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
	} {
		t.Run(scenario.name, func(t *testing.T) {
			run := func(t *testing.T, endpoint harness.Endpoint, model string) outcome {
				answer, exchanges, fixture := runClaude(t, binary, endpoint, model, scenario.prompt, scenario.tools, scenario.input)
				tools := checkExchanges(t, exchanges, model)
				if scenario.input != "" {
					data, err := os.ReadFile(filepath.Join(fixture, "output.txt"))
					if err != nil {
						t.Fatalf("Claude Code did not write output.txt: %v", err)
					}
					answer = string(data)
					if !reflect.DeepEqual(tools, []string{"Edit", "Read"}) {
						t.Errorf("expected completed Read and Edit tool calls, got %v", tools)
					}
				} else if len(tools) > 0 {
					t.Errorf("text-only scenario unexpectedly called tools: %v", tools)
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
			for _, model := range anthropic.DefaultModels() {
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

// Compare task results and completed tool names, not generated prose, IDs,
// token counts, number of requests, or SSE chunk boundaries.
type outcome struct {
	Answer string
	Tools  []string
}
