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

func TestParseEnvelopeOperationsMultiFile(t *testing.T) {
	envelope := "*** Begin Patch\n" +
		"*** Add File: a.go\n" +
		"+package a\n" +
		"*** Update File: b.go\n" +
		"*** Move to: c.go\n" +
		"@@\n" +
		"-old\n" +
		"+new\n" +
		"*** Delete File: d.go\n" +
		"*** End Patch\n"

	ops := ParseEnvelopeOperations(envelope)

	if len(ops) != 3 {
		t.Fatalf("ops = %+v, want 3", ops)
	}

	if ops[0].Type != "create_file" || ops[0].Path != "a.go" || ops[0].Diff != "+package a\n" {
		t.Errorf("ops[0] = %+v", ops[0])
	}
	if ops[1].Type != "update_file" || ops[1].Path != "b.go" || ops[1].Diff != "@@\n-old\n+new\n" {
		t.Errorf("ops[1] = %+v", ops[1])
	}
	if ops[2].Type != "delete_file" || ops[2].Path != "d.go" || ops[2].Diff != "" {
		t.Errorf("ops[2] = %+v", ops[2])
	}

	if first := ParseEnvelope(envelope); first.Path != "a.go" {
		t.Errorf("ParseEnvelope first op = %+v", first)
	}
}

func TestIsEnvelope(t *testing.T) {
	if !IsEnvelope("*** Begin Patch\n*** Delete File: x\n*** End Patch\n") {
		t.Error("envelope not detected")
	}
	if IsEnvelope(`{"type":"update_file","path":"x","diff":"@@\n"}`) {
		t.Error("JSON args misdetected as envelope")
	}
}

func editorSchemaEnum(t *testing.T, params map[string]any, property string) []string {
	t.Helper()

	props, _ := params["properties"].(map[string]any)
	field, _ := props[property].(map[string]any)
	values, _ := field["enum"].([]string)

	if len(values) == 0 {
		t.Fatalf("no enum on property %q", property)
	}

	return values
}

func requireSameSet(t *testing.T, got, want []string, label string) {
	t.Helper()

	have := map[string]bool{}
	for _, v := range got {
		have[v] = true
	}

	for _, v := range want {
		if !have[v] {
			t.Errorf("%s is missing %q (have %v)", label, v, got)
		}

		delete(have, v)
	}

	for v := range have {
		t.Errorf("%s advertises %q, which the native tool does not accept", label, v)
	}
}

// text_editor_20250728 accepts view, create, str_replace and insert.
// undo_edit was removed after text_editor_20250429 and must not reappear here.
func TestFunctionToolCoversNativeEditorCommands(t *testing.T) {
	tool := FunctionTool(provider.Tool{Kind: provider.ToolKindTextEditor, Name: NameTextEditor})

	requireSameSet(t, editorSchemaEnum(t, tool.Parameters, "command"),
		[]string{"view", "create", "str_replace", "insert"}, "text editor command enum")

	props, _ := tool.Parameters["properties"].(map[string]any)

	for _, param := range []string{"path", "view_range", "file_text", "old_str", "new_str", "insert_line", "insert_text"} {
		if _, ok := props[param]; !ok {
			t.Errorf("text editor schema is missing the %q parameter", param)
		}
	}
}

// The apply_patch operation union is create_file, update_file and delete_file.
// diff is required on the first two and absent on delete_file, which a single
// flat schema can only express by leaving diff optional.
func TestFunctionToolCoversNativeApplyPatchOperations(t *testing.T) {
	tool := FunctionTool(provider.Tool{Kind: provider.ToolKindTextEditor, Name: NameApplyPatch})

	requireSameSet(t, editorSchemaEnum(t, tool.Parameters, "type"),
		[]string{"create_file", "update_file", "delete_file"}, "apply_patch type enum")

	required, _ := tool.Parameters["required"].([]string)

	for _, name := range required {
		if name == "diff" {
			t.Error("diff must stay optional — delete_file carries no diff")
		}
	}

	if len(required) != 2 || required[0] != "type" || required[1] != "path" {
		t.Errorf("expected type and path to be required, got %v", required)
	}
}
