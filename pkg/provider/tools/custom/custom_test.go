package custom

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestFunctionToolKeepsNameAndSingleStringParameter(t *testing.T) {
	tool := FunctionTool(provider.Tool{
		Kind:        provider.ToolKindCustom,
		Name:        "apply_patch",
		Description: "Edit files.",
	})

	if tool.Kind != provider.ToolKindFunction {
		t.Fatalf("expected a plain function tool, got kind %q", tool.Kind)
	}

	if tool.Name != "apply_patch" {
		t.Fatalf("expected name to round-trip, got %q", tool.Name)
	}

	props, _ := tool.Parameters["properties"].(map[string]any)

	if len(props) != 1 {
		t.Fatalf("expected exactly one parameter, got %d", len(props))
	}

	input, _ := props[InputParameter].(map[string]any)

	if input["type"] != "string" {
		t.Fatalf("expected the input parameter to be a string, got %v", input["type"])
	}

	required, _ := tool.Parameters["required"].([]string)

	if len(required) != 1 || required[0] != InputParameter {
		t.Fatalf("expected input to be required, got %v", required)
	}
}

// The emulated schema stays deliberately loose: no additionalProperties:false
// and no strict-only keywords, so it survives backends without strict tool
// support (Bedrock ignores strict on Claude models).
func TestFunctionToolSchemaIsNotStrict(t *testing.T) {
	tool := FunctionTool(provider.Tool{Name: "edit"})

	if _, ok := tool.Parameters["additionalProperties"]; ok {
		t.Fatal("emulated schema must not set additionalProperties")
	}

	if tool.Strict != nil {
		t.Fatal("emulated schema must not request strict validation")
	}
}

func TestFunctionToolCarriesGrammarIntoDescription(t *testing.T) {
	tool := FunctionTool(provider.Tool{
		Name: "apply_patch",
		Format: &provider.ToolFormat{
			Type:       "grammar",
			Syntax:     "lark",
			Definition: "start: begin_patch",
		},
	})

	props, _ := tool.Parameters["properties"].(map[string]any)
	input, _ := props[InputParameter].(map[string]any)
	description, _ := input["description"].(string)

	if !strings.Contains(description, "lark") || !strings.Contains(description, "start: begin_patch") {
		t.Fatalf("expected the grammar in the parameter description, got %q", description)
	}
}

func TestWrapUnwrapRoundTrip(t *testing.T) {
	inputs := []string{
		"",
		"*** Begin Patch\n*** Add File: a.py\n+print(\"hi\")\n*** End Patch\n",
		"quotes \" backslash \\ newline \n unicode ä — tab \t",
		`{"looks":"like json"}`,
	}

	for _, input := range inputs {
		wrapped := Wrap(input)

		if !json.Valid([]byte(wrapped)) {
			t.Fatalf("Wrap produced invalid JSON for %q: %s", input, wrapped)
		}

		if !IsWrapped(wrapped) {
			t.Fatalf("IsWrapped returned false for wrapped %q", input)
		}

		if got := Unwrap(wrapped); got != input {
			t.Fatalf("round-trip mismatch:\n want %q\n got  %q", input, got)
		}
	}
}

// A backend running the tool natively yields freeform text, which must survive
// an Unwrap call untouched.
func TestUnwrapPassesThroughUnwrappedInput(t *testing.T) {
	tests := []string{
		"*** Begin Patch\n*** End Patch\n",
		"plain text",
		"",
		`{"type":"update_file","path":"main.go"}`,
		`{"input":"a","path":"b"}`,
		`{"other":"value"}`,
		"{not json",
	}

	for _, args := range tests {
		if IsWrapped(args) {
			t.Fatalf("IsWrapped must not match %q", args)
		}

		if got := Unwrap(args); got != args {
			t.Fatalf("Unwrap must pass through %q, got %q", args, got)
		}
	}
}

func TestUnwrapRejectsNonStringInput(t *testing.T) {
	args := `{"input":{"nested":true}}`

	if IsWrapped(args) {
		t.Fatal("a non-string input must not count as wrapped")
	}

	if got := Unwrap(args); got != args {
		t.Fatalf("expected pass-through, got %q", got)
	}
}

// defer_loading describes the declaration, not the input shape, so it has to
// survive the downgrade — a deferred tool that becomes eager changes which
// tools the model sees up front.
// Source: CustomToolParam, openai-go v3.
func TestFunctionToolPreservesDeferLoading(t *testing.T) {
	deferred := true

	tool := FunctionTool(provider.Tool{
		Kind:     provider.ToolKindCustom,
		Name:     "lookup",
		Deferred: &deferred,
	})

	if tool.Deferred == nil || !*tool.Deferred {
		t.Fatal("defer_loading must survive the downgrade")
	}
}

func TestFunctionToolPreservesNamespace(t *testing.T) {
	tool := FunctionTool(provider.Tool{
		Kind:      provider.ToolKindCustom,
		Name:      "edit",
		Namespace: "files",
	})

	if tool.Namespace != "files" {
		t.Fatalf("expected namespace to survive, got %q", tool.Namespace)
	}
}

// The native default format is unconstrained text, so a tool with no format
// still gets a usable description rather than an empty one.
func TestFunctionToolDescribesUnconstrainedText(t *testing.T) {
	tool := FunctionTool(provider.Tool{Name: "notes"})

	if tool.Description == "" {
		t.Fatal("expected a fallback description")
	}

	props, _ := tool.Parameters["properties"].(map[string]any)
	input, _ := props[InputParameter].(map[string]any)
	description, _ := input["description"].(string)

	if !strings.Contains(description, "raw text") {
		t.Fatalf("expected the unconstrained-text description, got %q", description)
	}
}

func TestIsEmulatedMatchesDeclaredCustomTools(t *testing.T) {
	tools := []provider.Tool{
		{Kind: provider.ToolKindCustom, Name: "apply_patch"},
		{Kind: provider.ToolKindFunction, Name: "get_weather"},
		{Name: "files", Tools: []provider.Tool{
			{Kind: provider.ToolKindCustom, Name: "edit"},
			{Kind: provider.ToolKindFunction, Name: "list"},
		}},
	}

	for _, name := range []string{"apply_patch", "files_edit"} {
		if !IsEmulated(tools, name) {
			t.Errorf("expected %q to be recognized as a custom tool", name)
		}
	}

	for _, name := range []string{"get_weather", "files_list", "unknown", ""} {
		if IsEmulated(tools, name) {
			t.Errorf("expected %q not to be recognized as a custom tool", name)
		}
	}
}

// A stream that ends mid-call leaves a wrapper the model never closed. Unwrap
// passes it through by design, so the flush path must use UnwrapPartial or it
// would hand `{"input":"...` to a client expecting freeform text.
func TestUnwrapPartialRecoversTruncatedWrapper(t *testing.T) {
	tests := []struct {
		args string
		want string
	}{
		{`{"input":"print("}`, `print(`},
		{`{"input":"print(`, `print(`},
		{`{"input":"print(\"a — b\")`, `print("a — b")`},
		{`{"input":"line1\nline2`, "line1\nline2"},
		{`{"input": "spaced`, `spaced`},
		{`{"input":"trailing escape\`, `trailing escape`},
		{`{"input":"partial unicode \u26`, `partial unicode `},
	}

	for _, tt := range tests {
		got, ok := UnwrapPartial(tt.args)

		if !ok {
			t.Errorf("UnwrapPartial(%q) reported nothing recoverable", tt.args)
			continue
		}

		if got != tt.want {
			t.Errorf("UnwrapPartial(%q):\n want %q\n got  %q", tt.args, tt.want, got)
		}

		if strings.Contains(got, `"`+InputParameter+`"`) {
			t.Errorf("UnwrapPartial(%q) leaked the wrapper: %q", tt.args, got)
		}
	}
}

// Nothing recoverable must yield ok=false so callers emit nothing rather than
// leaking a fragment of the wrapper.
func TestUnwrapPartialRejectsUnrecoverable(t *testing.T) {
	for _, args := range []string{"", "{", `{"in`, `{"input"`, `{"input":`, `{"other":"x"}`, "not json"} {
		if got, ok := UnwrapPartial(args); ok {
			t.Errorf("UnwrapPartial(%q) should be unrecoverable, got %q", args, got)
		}
	}
}

// A complete wrapper must behave exactly like Unwrap.
func TestUnwrapPartialMatchesUnwrapWhenComplete(t *testing.T) {
	input := "print(\"a — b\")\nx = r\"C:\\temp\\z.txt\"\n"

	got, ok := UnwrapPartial(Wrap(input))

	if !ok || got != input {
		t.Fatalf("UnwrapPartial on a complete wrapper: ok=%v got %q", ok, got)
	}
}
