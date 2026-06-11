package shell

import (
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestBashInput(t *testing.T) {
	tests := []struct {
		args string
		want string
	}{
		{`{"command":"ls -la"}`, "ls -la"},
		{`{"commands":["cd /tmp","ls"]}`, "cd /tmp\nls"},
		{`{"command":["bash","-lc","make test"]}`, "make test"},
		{`{"command":["git","status"]}`, "git status"},
	}

	for _, tt := range tests {
		input := BashInput(tt.args)
		if input["command"] != tt.want {
			t.Errorf("BashInput(%s) = %q, want %q", tt.args, input["command"], tt.want)
		}
	}
}

func TestShellAction(t *testing.T) {
	action := ShellAction(`{"command":"ls -la"}`)

	if !reflect.DeepEqual(action["commands"], []string{"ls -la"}) {
		t.Fatalf("commands: %+v", action["commands"])
	}

	passthrough := ShellAction(`{"commands":["a","b"],"timeout_ms":500}`)
	if passthrough["timeout_ms"] != float64(500) {
		t.Fatalf("timeout: %+v", passthrough)
	}
}

func TestLocalShellAction(t *testing.T) {
	action := LocalShellAction(`{"command":"make test"}`)

	if action["type"] != "exec" {
		t.Fatalf("type: %v", action["type"])
	}
	if !reflect.DeepEqual(action["command"], []string{"bash", "-lc", "make test"}) {
		t.Fatalf("command: %+v", action["command"])
	}

	passthrough := LocalShellAction(`{"command":["git","status"],"working_directory":"/repo"}`)
	if passthrough["working_directory"] != "/repo" {
		t.Fatalf("passthrough: %+v", passthrough)
	}
}

func TestOutputText(t *testing.T) {
	text := OutputText(`[{"stdout":"hello\n","stderr":"warn","outcome":{"type":"exit","exit_code":2}}]`)

	want := "hello\nwarn\n(exit code 2)"
	if text != want {
		t.Fatalf("text: %q, want %q", text, want)
	}

	if OutputText("plain output") != "plain output" {
		t.Fatal("plain text should pass through")
	}
}

func TestFunctionTool(t *testing.T) {
	for _, name := range []string{NameBash, NameShell, NameLocalShell} {
		tool := FunctionTool(provider.Tool{Kind: provider.ToolKindShell, Name: name})

		if tool.Kind != provider.ToolKindFunction || tool.Name != name {
			t.Fatalf("%s tool: %+v", name, tool)
		}
		if tool.Description == "" || tool.Parameters == nil {
			t.Fatalf("%s tool missing description or schema", name)
		}
	}
}
