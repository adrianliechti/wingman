package claudecode

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/test/harness"
)

const projectTestCommand = harness.ProjectTestCommand

func projectScenario(t *testing.T) claudeScenario {
	project := harness.NewProjectFixture(t)
	return claudeScenario{
		name:  "project_repair",
		tools: "Read,Edit,Bash",
		want:  "WINGMAN_PROJECT_OK",
		prompt: `Fix the invoice project in the current working directory.
First run "python3 -m unittest -v" with Bash in the project directory and observe the failures before editing anything. Run commands in the foreground, without shell wrappers or redirection.
Use Read to inspect pricing.py, invoice.py, test_invoice.py, order.json and expected.json. For Read pass only file_path.
Use Edit to fix both pricing.py and invoice.py. The subtotal is quantity times the discounted unit price, with each unit price floored at zero. Cancelled lines contribute neither units nor money. item_count counts units, and order_id must be preserved.
Do not edit test_invoice.py, order.json or expected.json. After your edits, rerun exactly "python3 -m unittest -v" and fix any remaining failures. Reply with exactly WINGMAN_PROJECT_OK only after all seven tests pass.`,
		options: claudeRunOptions{
			maxTurns:     12,
			timeout:      3 * time.Minute,
			allowedTools: "Read,Edit,Bash(" + projectTestCommand + ")",
			setup:        project.Setup,
		},
		verify: func(t *testing.T, exchanges []exchange, dir string) {
			steps, err := projectSteps(exchanges)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateProjectWorkflow(steps, dir); err != nil {
				t.Error(err)
			}
			project.Verify(t, dir)
			t.Logf("verified %d completed tool calls, failure before edits, repairs in both modules, and seven passing Python tests", len(steps))
		},
	}
}

type projectStep struct {
	name, file, command string
	isError             bool
	result              string
}

// Collect each returned call once, in execution order, from replayed history.
func projectSteps(exchanges []exchange) ([]projectStep, error) {
	calls := map[string]projectStep{}
	seen := map[string]bool{}
	var steps []projectStep
	for _, exchange := range exchanges {
		u, err := url.Parse(exchange.Path)
		if err != nil || u.Path != "/v1/messages" {
			continue
		}
		var request struct {
			Messages []struct {
				Content jsontext.Value `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal([]byte(exchange.Request), &request); err != nil {
			return nil, err
		}
		for _, message := range request.Messages {
			var blocks []struct {
				Type      string         `json:"type"`
				ID        string         `json:"id"`
				Name      string         `json:"name"`
				ToolUseID string         `json:"tool_use_id"`
				IsError   bool           `json:"is_error"`
				Content   jsontext.Value `json:"content"`
				Input     struct {
					FilePath string `json:"file_path"`
					Command  string `json:"command"`
				} `json:"input"`
			}
			if json.Unmarshal(message.Content, &blocks) != nil {
				continue // String user prompts contain no tool events.
			}
			for _, block := range blocks {
				switch block.Type {
				case "tool_use":
					calls[block.ID] = projectStep{name: block.Name, file: filepath.Base(block.Input.FilePath), command: block.Input.Command}
				case "tool_result":
					if seen[block.ToolUseID] {
						continue
					}
					step, ok := calls[block.ToolUseID]
					if !ok {
						return nil, fmt.Errorf("unmatched project tool result %q", block.ToolUseID)
					}
					step.isError, step.result = block.IsError, string(block.Content)
					steps = append(steps, step)
					seen[block.ToolUseID] = true
				}
			}
		}
	}
	return steps, nil
}

func validateProjectWorkflow(steps []projectStep, dir string) error {
	failedTests, passedAfterEdits := false, false
	edited := map[string]bool{}
	for _, step := range steps {
		switch step.name {
		case "Edit":
			if !step.isError {
				if !failedTests {
					return fmt.Errorf("project was edited before observing failing tests")
				}
				edited[step.file] = true
				passedAfterEdits = false
			}
		case "Bash":
			if !isProjectTestCommand(step.command, dir) {
				// The CLI can deny an attempted command. It did not execute and
				// cannot establish either the failing or passing test evidence.
				if step.isError {
					continue
				}
				return fmt.Errorf("unexpected project command %q", step.command)
			}
			if step.isError && strings.Contains(step.result, "FAILED") {
				failedTests = true
			}
			passedAfterEdits = !step.isError && strings.Contains(step.result, "Ran 7 tests") && strings.Contains(step.result, "OK") && edited["pricing.py"] && edited["invoice.py"]
		}
	}
	if !failedTests || !passedAfterEdits {
		return fmt.Errorf("expected failing tests, edits to pricing.py and invoice.py, then passing tests (failed=%t, edited=%v, passed=%t)", failedTests, edited, passedAfterEdits)
	}
	return nil
}

// Some models explicitly enter the already-current project directory.
func isProjectTestCommand(command, dir string) bool {
	command = strings.TrimSpace(command)
	if command == projectTestCommand {
		return true
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	for _, path := range []string{dir, resolved} {
		if path == "" {
			continue
		}
		for _, quoted := range []string{path, "'" + path + "'", `"` + path + `"`} {
			if command == "cd "+quoted+" && "+projectTestCommand {
				return true
			}
		}
	}
	return false
}

func TestProjectWorkflow(t *testing.T) {
	dir := t.TempDir()
	failure := projectStep{name: "Bash", command: projectTestCommand, isError: true, result: "Ran 7 tests\nFAILED (failures=6)"}
	pricing := projectStep{name: "Edit", file: "pricing.py"}
	invoice := projectStep{name: "Edit", file: "invoice.py"}
	success := projectStep{name: "Bash", command: projectTestCommand, result: "Ran 7 tests\nOK"}
	withCD := success
	withCD.command = "cd '" + dir + "' && " + projectTestCommand
	wrongDir := success
	wrongDir.command = "cd /unrelated-project && " + projectTestCommand
	for _, tc := range []struct {
		name  string
		steps []projectStep
		valid bool
	}{
		{"repair", []projectStep{failure, pricing, invoice, success}, true},
		{"explicit project directory", []projectStep{failure, pricing, invoice, withCD}, true},
		{"wrong project directory", []projectStep{failure, pricing, invoice, wrongDir}, false},
		{"no initial failure", []projectStep{pricing, invoice, success}, false},
		{"one module only", []projectStep{failure, pricing, success}, false},
		{"no rerun", []projectStep{failure, pricing, invoice}, false},
		{"edit after passing tests", []projectStep{failure, pricing, invoice, success, pricing}, false},
		{"tests still fail", []projectStep{failure, pricing, invoice, failure}, false},
		{"other command", []projectStep{{name: "Bash", command: "echo OK"}}, false},
		{"denied command then repair", []projectStep{failure, {name: "Bash", command: "git status", isError: true}, pricing, invoice, success}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateProjectWorkflow(tc.steps, dir); (err == nil) != tc.valid {
				t.Fatalf("valid=%t, error=%v", tc.valid, err)
			}
		})
	}
}

func TestProjectStepsDeduplicatesReplayedHistory(t *testing.T) {
	request := `{"messages":[{"role":"user","content":"Fix it"},{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"Bash","input":{"command":"python3 -m unittest -v"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","is_error":true,"content":"Ran 7 tests\nFAILED"}]}]}`
	record := exchange{Path: "/v1/messages?beta=true", Request: request}
	steps, err := projectSteps([]exchange{record, record})
	if err != nil || len(steps) != 1 || steps[0].name != "Bash" || !steps[0].isError || !strings.Contains(steps[0].result, "FAILED") {
		t.Fatalf("incorrect tool history: %+v, %v", steps, err)
	}
}
