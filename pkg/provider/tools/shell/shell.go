// Package shell bridges the model-native command-execution tool dialects:
// Anthropic's bash tool ({command, restart}), OpenAI's shell tool
// (shell_call items with a commands list), and OpenAI's legacy local_shell
// tool (local_shell_call items with an exec argv).
//
// A shell tool keeps the dialect of the client that registered it
// (provider.Tool.Name — the three dialects use distinct names). Backends with
// a native tool of the same dialect use it directly; all other backends
// emulate the tool as a plain function tool in the client's dialect (see
// FunctionTool). The converters cover replaying mixed histories.
package shell

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

const (
	NameBash       = "bash"
	NameShell      = "shell"
	NameLocalShell = "local_shell"
)

// BashInput renders shell ToolCall arguments of any dialect as a bash tool
// input ({command, restart}).
func BashInput(args string) map[string]any {
	var input map[string]any
	json.Unmarshal([]byte(args), &input)

	if input == nil {
		return map[string]any{}
	}

	if _, ok := input["command"].(string); ok {
		return input
	}

	if commands := stringList(input["commands"]); len(commands) > 0 {
		return map[string]any{"command": strings.Join(commands, "\n")}
	}

	if argv := stringList(input["command"]); len(argv) > 0 {
		return map[string]any{"command": argvToCommand(argv)}
	}

	return input
}

// Commands renders shell ToolCall arguments of any dialect as the command
// list of the shell dialect.
func Commands(args string) []string {
	var input map[string]any
	json.Unmarshal([]byte(args), &input)

	if commands := stringList(input["commands"]); len(commands) > 0 {
		return commands
	}

	if command, ok := input["command"].(string); ok && command != "" {
		return []string{command}
	}

	if argv := stringList(input["command"]); len(argv) > 0 {
		return []string{argvToCommand(argv)}
	}

	return nil
}

// argvToCommand renders an exec argv as a shell command line. Wrappers like
// ["bash", "-lc", cmd] unwrap to their inner command.
func argvToCommand(argv []string) string {
	if len(argv) == 3 && (argv[0] == "bash" || argv[0] == "sh" || argv[0] == "zsh") &&
		(argv[1] == "-c" || argv[1] == "-lc") {
		return argv[2]
	}

	var parts []string
	for _, arg := range argv {
		if strings.ContainsAny(arg, " \t\"'") {
			parts = append(parts, fmt.Sprintf("%q", arg))
		} else {
			parts = append(parts, arg)
		}
	}

	return strings.Join(parts, " ")
}

func stringList(v any) []string {
	var result []string

	switch raw := v.(type) {
	case []any:
		for _, item := range raw {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
	case []string:
		result = raw
	}

	return result
}

// ShellAction renders shell ToolCall arguments of any dialect as a shell_call
// action ({commands, timeout_ms, max_output_length}).
func ShellAction(args string) map[string]any {
	var input map[string]any
	json.Unmarshal([]byte(args), &input)

	if input != nil && len(stringList(input["commands"])) > 0 {
		delete(input, "command")
		return input
	}

	return map[string]any{"commands": Commands(args)}
}

// LocalShellAction renders shell ToolCall arguments of any dialect as a
// local_shell_call exec action ({type, command, ...}).
func LocalShellAction(args string) map[string]any {
	var input map[string]any
	json.Unmarshal([]byte(args), &input)

	if input != nil && len(stringList(input["command"])) > 0 {
		input["type"] = "exec"
		delete(input, "commands")
		return input
	}

	var argv []string
	if commands := Commands(args); len(commands) > 0 {
		argv = []string{"bash", "-lc", strings.Join(commands, "\n")}
	}

	action := map[string]any{"type": "exec", "command": argv}

	if input != nil {
		for _, key := range []string{"timeout_ms", "working_directory", "env", "user"} {
			if v, ok := input[key]; ok {
				action[key] = v
			}
		}
	}

	return action
}

// OutputText flattens a shell_call_output content list
// ([{stdout, stderr, outcome}]) into plain text for backends that take text
// tool results. Plain-text output passes through.
func OutputText(output string) string {
	var chunks []struct {
		Stdout  string `json:"stdout"`
		Stderr  string `json:"stderr"`
		Outcome *struct {
			Type     string `json:"type"`
			ExitCode int    `json:"exit_code"`
		} `json:"outcome"`
	}

	if err := json.Unmarshal([]byte(output), &chunks); err != nil {
		return output
	}

	var b strings.Builder

	for _, c := range chunks {
		if c.Stdout != "" {
			b.WriteString(c.Stdout)
		}
		if c.Stderr != "" {
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
				b.WriteString("\n")
			}
			b.WriteString(c.Stderr)
		}
		if c.Outcome != nil {
			if c.Outcome.Type == "timeout" {
				b.WriteString("\n(command timed out)")
			}
			if c.Outcome.Type == "exit" && c.Outcome.ExitCode != 0 {
				b.WriteString(fmt.Sprintf("\n(exit code %d)", c.Outcome.ExitCode))
			}
		}
	}

	return b.String()
}

// FunctionTool renders a shell tool as a plain function tool in the same
// dialect, for backends without a native equivalent.
func FunctionTool(t provider.Tool) provider.Tool {
	switch t.Name {
	case NameShell:
		return provider.Tool{
			Name:        NameShell,
			Description: shellDescription,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"commands": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Shell commands to run in order.",
					},
					"timeout_ms": map[string]any{
						"type":        "integer",
						"description": "Maximum wall-clock time in milliseconds.",
					},
				},
				"required": []string{"commands"},
			},
		}

	case NameLocalShell:
		return provider.Tool{
			Name:        NameLocalShell,
			Description: shellDescription,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "The command to run as an argv list, e.g. [\"bash\", \"-lc\", \"ls -la\"].",
					},
					"timeout_ms": map[string]any{
						"type":        "integer",
						"description": "Maximum wall-clock time in milliseconds.",
					},
					"working_directory": map[string]any{
						"type":        "string",
						"description": "Directory to run the command in.",
					},
				},
				"required": []string{"command"},
			},
		}
	}

	return provider.Tool{
		Name:        NameBash,
		Description: bashDescription,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to run.",
				},
				"restart": map[string]any{
					"type":        "boolean",
					"description": "Restart the shell session instead of running a command.",
				},
			},
			"required": []string{"command"},
		},
	}
}

const bashDescription = `Run commands in a persistent bash session. ` +
	`State (working directory, environment variables) carries over between calls. ` +
	`Set restart to true to reset the session.`

const shellDescription = `Run shell commands in the workspace and return their combined stdout and stderr output.`
