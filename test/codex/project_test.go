package codex

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/test/harness"
)

const projectReadCommand = "cat pricing.py invoice.py test_invoice.py order.json expected.json"

func projectScenario(t *testing.T) codexScenario {
	project := harness.NewProjectFixture(t)
	return codexScenario{
		name: "project_repair",
		want: "WINGMAN_PROJECT_OK",
		prompt: `Fix the invoice project in the current working directory.
First use exec_command to run exactly "python3 -m unittest -v" and observe the failures before editing anything. Run commands in the foreground, without shell wrappers or redirection.
Then use exec_command to run exactly "cat pricing.py invoice.py test_invoice.py order.json expected.json" to inspect all five files.
Use apply_patch directly to fix both pricing.py and invoice.py. Use V4A patches with hunk headers containing only @@, without line numbers. The subtotal is quantity times the discounted unit price, with each unit price floored at zero. Cancelled lines contribute neither units nor money. item_count counts units, and order_id must be preserved.
Do not edit test_invoice.py, order.json or expected.json. Do not write files using shell commands or call other tools. After your edits, rerun exactly "python3 -m unittest -v" and fix any remaining failures. Reply with exactly WINGMAN_PROJECT_OK only after all seven tests pass.`,
		options: codexRunOptions{setup: project.Setup, timeout: 3 * time.Minute, maxRequests: 12},
		verify: func(t *testing.T, exchanges []harness.Exchange, events []cliEvent, dir string) {
			if err := validateProjectCommands(exchanges, dir); err != nil {
				t.Error(err)
			}
			steps, err := projectSteps(exchanges)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateProjectWorkflow(steps, dir); err != nil {
				t.Error(err)
			}
			// Require the CLI to report successful patches as well as returning
			// their results to the model.
			changed := map[string]bool{}
			for _, event := range events {
				if event.Type == "item.completed" && event.Item.Type == "file_change" && event.Item.Status == "completed" {
					for _, change := range event.Item.Changes {
						changed[filepath.Base(change.Path)] = true
					}
				}
			}
			if len(changed) != 2 || !changed["pricing.py"] || !changed["invoice.py"] {
				t.Errorf("expected CLI file changes for both source modules, got %v", changed)
			}
			project.Verify(t, dir)
			if !t.Failed() {
				t.Log("verified failing tests, source reads, patches to both modules, and seven passing Python tests")
			}
		},
	}
}

type projectStep struct {
	name, command, result string
	files                 []string
	exitCode              int
}

var projectExitCode = regexp.MustCompile(`(?m)^(?:Process exited with code |Exit code: )(-?\d+)\s*$`)

// Codex can omit a failed command from --json events even though its output
// reaches the model. Use the completed tool results, once per call ID, to
// reconstruct the workflow independently of that CLI presentation detail.
func projectSteps(exchanges []harness.Exchange) ([]projectStep, error) {
	calls := map[string]projectStep{}
	seen := map[string]bool{}
	var steps []projectStep
	for _, exchange := range exchanges {
		var request struct {
			Input []struct {
				Type      string `json:"type"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				Input     string `json:"input"`
				CallID    string `json:"call_id"`
				Output    string `json:"output"`
			} `json:"input"`
		}
		if err := json.Unmarshal([]byte(exchange.Request), &request); err != nil {
			return nil, err
		}
		for _, item := range request.Input {
			switch item.Type {
			case "function_call":
				var args struct {
					Cmd string `json:"cmd"`
				}
				if err := json.Unmarshal([]byte(item.Arguments), &args); err != nil {
					return nil, err
				}
				calls[item.CallID] = projectStep{name: item.Name, command: args.Cmd}
			case "custom_tool_call":
				step := projectStep{name: item.Name}
				for line := range strings.SplitSeq(item.Input, "\n") {
					for _, prefix := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: ", "*** Move to: "} {
						if file, ok := strings.CutPrefix(line, prefix); ok {
							step.files = append(step.files, file)
						}
					}
				}
				calls[item.CallID] = step
			case "function_call_output", "custom_tool_call_output":
				if seen[item.CallID] {
					continue
				}
				step, ok := calls[item.CallID]
				if !ok {
					return nil, fmt.Errorf("unmatched project tool result %q", item.CallID)
				}
				match := projectExitCode.FindStringSubmatch(item.Output)
				if match == nil {
					return nil, fmt.Errorf("project tool %s did not return an exit code: %.500s", step.name, item.Output)
				}
				code, err := strconv.Atoi(match[1])
				if err != nil {
					return nil, err
				}
				step.exitCode, step.result = code, item.Output
				steps = append(steps, step)
				seen[item.CallID] = true
			}
		}
	}
	return steps, nil
}

// Check original exec arguments as well as CLI output: commands must run the
// real fixture tests, and cannot manufacture output or rewrite tests in a shell.
func validateProjectCommands(exchanges []harness.Exchange, dir string) error {
	for _, exchange := range exchanges {
		var request struct {
			Input []struct {
				Type      string `json:"type"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"input"`
		}
		if err := json.Unmarshal([]byte(exchange.Request), &request); err != nil {
			return err
		}
		for _, item := range request.Input {
			switch item.Type {
			case "custom_tool_call":
				if item.Name != "apply_patch" {
					return fmt.Errorf("unexpected project tool %q", item.Name)
				}
			case "function_call":
				if item.Name != "exec_command" {
					return fmt.Errorf("unexpected project tool %q", item.Name)
				}
				var args struct {
					Cmd     string `json:"cmd"`
					Workdir string `json:"workdir"`
				}
				if err := json.Unmarshal([]byte(item.Arguments), &args); err != nil {
					return err
				}
				if args.Workdir != "" && args.Workdir != "." && filepath.Clean(args.Workdir) != dir {
					return fmt.Errorf("project command used the wrong directory %q", args.Workdir)
				}
				if !isProjectCommand(args.Cmd, dir, harness.ProjectTestCommand) && !isProjectCommand(args.Cmd, dir, projectReadCommand) {
					return fmt.Errorf("unexpected project command %q", args.Cmd)
				}
			}
		}
	}
	return nil
}

func isProjectCommand(command, dir, want string) bool {
	command = strings.TrimSpace(command)
	if command == want {
		return true
	}
	for _, quoted := range []string{dir, "'" + dir + "'", `"` + dir + `"`} {
		if command == "cd "+quoted+" && "+want {
			return true
		}
	}
	return false
}

func validateProjectWorkflow(steps []projectStep, dir string) error {
	failedTests, read, passedAfterEdits := false, false, false
	edited := map[string]bool{}
	for _, step := range steps {
		switch step.name {
		case "exec_command":
			if strings.Contains(step.command, projectReadCommand) && step.exitCode == 0 && strings.Contains(step.result, "def line_total(item):") && strings.Contains(step.result, "class InvoiceTests") {
				read = true
			}
			if strings.Contains(step.command, harness.ProjectTestCommand) {
				if step.exitCode != 0 && strings.Contains(step.result, "Ran 7 tests") && strings.Contains(step.result, "FAILED") {
					failedTests = true
				}
				passedAfterEdits = step.exitCode == 0 && strings.Contains(step.result, "Ran 7 tests") && strings.Contains(step.result, "\nOK") && edited["pricing.py"] && edited["invoice.py"]
			}
		case "apply_patch":
			if step.exitCode != 0 {
				continue
			}
			if !failedTests || !read {
				return fmt.Errorf("project was edited before observing failing tests and reading the source")
			}
			for _, file := range step.files {
				path := file
				if !filepath.IsAbs(path) {
					path = filepath.Join(dir, path)
				}
				name := filepath.Base(path)
				if filepath.Dir(path) != dir || (name != "pricing.py" && name != "invoice.py") {
					return fmt.Errorf("unexpected project edit %q", file)
				}
				edited[name] = true
			}
			passedAfterEdits = false
		}
	}
	if !failedTests || !read || !passedAfterEdits {
		return fmt.Errorf("expected failing tests, source reads, patches to both modules, then passing tests (failed=%t, read=%t, edited=%v, passed=%t)", failedTests, read, edited, passedAfterEdits)
	}
	return nil
}

func TestCodexOfflineProjectRepair(t *testing.T) {
	binary := codexBinary(t, false)
	scenario := projectScenario(t)
	patch := `*** Begin Patch
*** Update File: pricing.py
@@
-    return item["unit_price_cents"] - item.get("discount_cents", 0)
+    return item["quantity"] * max(0, item["unit_price_cents"] - item.get("discount_cents", 0))
*** Update File: invoice.py
@@
-    items = order["items"]
+    items = [item for item in order["items"] if not item.get("cancelled", False)]
@@
-        "item_count": len(items),
+        "item_count": sum(item["quantity"] for item in items),
*** End Patch`
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "POST" || req.URL.Path != "/v1/responses" {
			t.Errorf("unexpected upstream request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		calls++
		switch calls {
		case 1, 4:
			io.WriteString(w, toolStreamWithID("function_call", "exec_command", fmt.Sprintf("tests_%d", calls), `{"cmd":"python3 -m unittest -v","yield_time_ms":1000}`))
		case 2:
			input, _ := json.Marshal(map[string]any{"cmd": projectReadCommand})
			io.WriteString(w, toolStreamWithID("function_call", "exec_command", "read", string(input)))
		case 3:
			io.WriteString(w, toolStreamWithID("custom_tool_call", "apply_patch", "patch", patch))
		case 5:
			io.WriteString(w, strings.ReplaceAll(textStream(), "WINGMAN_E2E_OK", scenario.want))
		default:
			t.Errorf("unexpected extra CLI request %d", calls)
		}
	}))
	t.Cleanup(upstream.Close)
	actual, exchanges, dir, events := runCodexWithOptions(t, binary, harness.Endpoint{Name: "offline", BaseURL: upstream.URL + "/v1", APIKey: "test"}, "wingman-fixture", scenario.prompt, "", scenario.options)
	if actual != (outcome{Answer: scenario.want, Read: true, Edited: true}) {
		t.Errorf("unexpected CLI outcome: %+v", actual)
	}
	tools := checkExchanges(t, exchanges, "wingman-fixture")
	if len(tools) != 2 || !tools["exec_command"] || !tools["apply_patch"] {
		t.Errorf("unexpected project tools: %v", tools)
	}
	scenario.verify(t, exchanges, events, dir)
}

func TestProjectWorkflow(t *testing.T) {
	dir := t.TempDir()
	patch := projectStep{name: "apply_patch", files: []string{"pricing.py", "invoice.py"}}
	failure := projectStep{name: "exec_command", command: harness.ProjectTestCommand, result: "Ran 7 tests\nFAILED (failures=6)", exitCode: 1}
	read := projectStep{name: "exec_command", command: projectReadCommand, result: "def line_total(item):\nclass InvoiceTests"}
	success := projectStep{name: "exec_command", command: harness.ProjectTestCommand, result: "Ran 7 tests\nOK"}
	failedPatch := patch
	failedPatch.exitCode = 1
	oneModule := patch
	oneModule.files = oneModule.files[:1]
	protected := projectStep{name: "apply_patch", files: []string{"test_invoice.py"}}
	for _, tc := range []struct {
		name  string
		steps []projectStep
		valid bool
	}{
		{"repair", []projectStep{failure, read, patch, success}, true},
		{"no initial failure", []projectStep{read, patch, success}, false},
		{"no source read", []projectStep{failure, patch, success}, false},
		{"one module only", []projectStep{failure, read, oneModule, success}, false},
		{"failed patch", []projectStep{failure, read, failedPatch, success}, false},
		{"no rerun", []projectStep{failure, read, patch}, false},
		{"edit after passing tests", []projectStep{failure, read, patch, success, patch}, false},
		{"tests still fail", []projectStep{failure, read, patch, failure}, false},
		{"protected file edited", []projectStep{failure, read, patch, protected, success}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateProjectWorkflow(tc.steps, dir); (err == nil) != tc.valid {
				t.Fatalf("valid=%t, error=%v", tc.valid, err)
			}
		})
	}
}

func TestProjectCommands(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, command, workdir string
		valid                  bool
	}{
		{"tests", harness.ProjectTestCommand, dir, true},
		{"read", projectReadCommand, "", true},
		{"explicit directory", "cd '" + dir + "' && " + harness.ProjectTestCommand, dir, true},
		{"wrong directory", harness.ProjectTestCommand, "/unrelated-project", false},
		{"different tests", "python3 -m unittest -v test_other", dir, false},
		{"fake output", "echo 'Ran 7 tests\nOK'", dir, false},
		{"shell edits", "echo replacement > test_invoice.py", dir, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, _ := json.Marshal(map[string]any{"cmd": tc.command, "workdir": tc.workdir})
			request, _ := json.Marshal(map[string]any{"input": []any{map[string]any{"type": "function_call", "name": "exec_command", "arguments": string(args)}}})
			if err := validateProjectCommands([]harness.Exchange{{Request: string(request)}}, dir); (err == nil) != tc.valid {
				t.Fatalf("valid=%t, error=%v", tc.valid, err)
			}
		})
	}
}
