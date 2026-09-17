package codex

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

func TestCodex(t *testing.T) {
	if os.Getenv("CODEX_LIVE") != "1" {
		t.Skip("set CODEX_LIVE=1 to run paid Codex comparison tests")
	}
	h := openai.New(t)
	binary := codexBinary(t, true)
	version, err := exec.Command(binary, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Codex version: %s", version)
	input := fmt.Sprintf("fixture-%s\n", rand.Text())
	for _, scenario := range []struct {
		name, prompt, input, want string
	}{
		{"text", "Reply with exactly WINGMAN_E2E_OK and nothing else. Do not use tools.", "", "WINGMAN_E2E_OK"},
		{"read_edit", "Use one shell call with `cat input.txt output.txt` to read both files. Then call the apply_patch tool directly to replace REPLACE_ME in output.txt with the text from input.txt. Use a V4A patch with a hunk header containing only @@, without line numbers. Preserve the final newline so both files contain exactly the same bytes. Do not write files using shell commands. Do not use other tools. Reply with DONE after editing the file.", input, input},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			run := func(t *testing.T, endpoint harness.Endpoint, model string) outcome {
				actual, exchanges, fixture := runCodex(t, binary, endpoint, model, scenario.prompt, scenario.input)
				tools := checkExchanges(t, exchanges, model)
				if scenario.input != "" {
					data, err := os.ReadFile(filepath.Join(fixture, "output.txt"))
					if err != nil {
						t.Fatal(err)
					}
					actual.Answer = string(data)
					if !actual.Read || !actual.Edited || !tools["apply_patch"] || !(tools["exec_command"] || tools["shell_command"] || tools["shell"]) {
						t.Errorf("expected a completed shell read and apply_patch edit: %+v, calls: %v", actual, tools)
					}
				} else if actual.Read || actual.Edited || len(tools) > 0 {
					t.Errorf("text-only scenario unexpectedly used tools: %v", tools)
				}
				if actual.Answer != scenario.want {
					t.Errorf("outcome %q, want %q", actual.Answer, scenario.want)
				}
				return actual
			}
			var reference outcome
			if !t.Run("openai", func(t *testing.T) {
				reference = run(t, h.OpenAI, h.ReferenceModel)
			}) {
				t.Fatal("OpenAI reference failed; cannot compare Wingman")
			}
			for _, model := range openai.DefaultModels() {
				t.Run("wingman/"+model.Name, func(t *testing.T) {
					h.SkipUnlessConfigured(t, model.Name)
					actual := run(t, h.Wingman, model.Name)
					if actual != reference {
						t.Errorf("outcome differs from OpenAI: got %+v, want %+v", actual, reference)
					}
				})
			}
		})
	}
}
