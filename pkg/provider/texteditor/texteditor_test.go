package texteditor

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestOperationToInput_Create(t *testing.T) {
	op := Operation{
		Type: "create_file",
		Path: "hello.py",
		Diff: "+def main():\n+    print(\"hi\")\n",
	}

	in := op.Input()

	if in.Command != "create" || in.Path != "hello.py" {
		t.Fatalf("input: %+v", in)
	}
	if in.FileText != "def main():\n    print(\"hi\")" {
		t.Fatalf("file_text: %q", in.FileText)
	}
}

func TestOperationToInput_UpdateKeepsContext(t *testing.T) {
	op := Operation{
		Type: "update_file",
		Path: "fib.py",
		Diff: "@@ def fib(n):\n     if n <= 1:\n-        return n\n+        return max(n, 0)\n     return fib(n - 1) + fib(n - 2)\n",
	}

	in := op.Input()

	if in.Command != "str_replace" {
		t.Fatalf("command: %q", in.Command)
	}

	wantOld := "    if n <= 1:\n        return n\n    return fib(n - 1) + fib(n - 2)"
	wantNew := "    if n <= 1:\n        return max(n, 0)\n    return fib(n - 1) + fib(n - 2)"

	if in.OldStr != wantOld {
		t.Errorf("old_str:\n%q\nwant:\n%q", in.OldStr, wantOld)
	}
	if in.NewStr != wantNew {
		t.Errorf("new_str:\n%q\nwant:\n%q", in.NewStr, wantNew)
	}
}

func TestOperationToInput_Delete(t *testing.T) {
	in := Operation{Type: "delete_file", Path: "old.py"}.Input()

	if in.Command != "view" || in.Path != "old.py" {
		t.Fatalf("input: %+v", in)
	}
}

func TestInputToOperation_Create(t *testing.T) {
	op := Input{Command: "create", Path: "a.go", FileText: "package a\n"}.Operation()

	if op.Type != "create_file" || op.Path != "a.go" || op.Diff != "+package a\n" {
		t.Fatalf("operation: %+v", op)
	}
}

func TestInputToOperation_StrReplace(t *testing.T) {
	op := Input{Command: "str_replace", Path: "a.go", OldStr: "old", NewStr: "new"}.Operation()

	if op.Type != "update_file" || op.Diff != "@@\n-old\n+new\n" {
		t.Fatalf("operation: %+v", op)
	}
}

func TestInputToOperation_Insert(t *testing.T) {
	line := 0
	op := Input{Command: "insert", Path: "a.go", InsertLine: &line, InsertText: "// header\n"}.Operation()

	if op.Type != "update_file" || op.Diff != "+// header\n" {
		t.Fatalf("operation: %+v", op)
	}
}

func TestRoundTripStrReplace(t *testing.T) {
	in := Input{Command: "str_replace", Path: "a.go", OldStr: "foo\nbar", NewStr: "foo\nbaz"}

	back := in.Operation().Input()

	if back.Command != "str_replace" || back.OldStr != in.OldStr || back.NewStr != in.NewStr {
		t.Fatalf("round trip: %+v", back)
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	ops := []Operation{
		{Type: "update_file", Path: "main.go", Diff: "@@\n-old\n+new\n"},
		{Type: "create_file", Path: "new.go", Diff: "+package main\n"},
		{Type: "delete_file", Path: "gone.go"},
	}

	for _, op := range ops {
		got := ParseEnvelope(op.Envelope())

		if got.Type != op.Type || got.Path != op.Path || got.Diff != op.Diff {
			t.Errorf("round trip %s: got %+v, want %+v", op.Type, got, op)
		}
	}
}

func TestParseOperationArgs(t *testing.T) {
	op := ParseOperation(`{"type":"update_file","path":"main.go","diff":"@@\n-a\n+b\n"}`)

	if op.Type != "update_file" || op.Path != "main.go" || op.Diff != "@@\n-a\n+b\n" {
		t.Fatalf("operation: %+v", op)
	}

	back := ParseOperation(op.Args())
	if back != op {
		t.Fatalf("args round trip: %+v", back)
	}
}

func TestFunctionTool(t *testing.T) {
	applyPatch := FunctionTool(provider.Tool{Kind: provider.ToolKindTextEditor, Name: NameApplyPatch})

	if applyPatch.Kind != provider.ToolKindFunction || applyPatch.Name != NameApplyPatch {
		t.Fatalf("apply_patch tool: %+v", applyPatch)
	}
	if applyPatch.Description == "" || applyPatch.Parameters == nil {
		t.Fatal("apply_patch tool missing description or schema")
	}

	editor := FunctionTool(provider.Tool{Kind: provider.ToolKindTextEditor, Name: NameTextEditor})

	if editor.Kind != provider.ToolKindFunction || editor.Name != NameTextEditor {
		t.Fatalf("text editor tool: %+v", editor)
	}
	if editor.Description == "" || editor.Parameters == nil {
		t.Fatal("text editor tool missing description or schema")
	}
}
