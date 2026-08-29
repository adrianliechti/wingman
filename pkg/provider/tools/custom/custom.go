// Package custom emulates OpenAI's freeform custom tools on backends that only
// accept JSON-schema function tools (Anthropic, Bedrock, Google, and the
// OpenAI Chat Completions surface).
//
// A freeform tool takes raw text, not a JSON object. Backends without a native
// equivalent declare it as a function tool with a single string parameter (see
// FunctionTool); the call's freeform text then travels as that parameter's
// value. Wrap and Unwrap convert between the two representations at the
// backend boundary, so a client that registered a custom tool keeps seeing
// freeform input end-to-end.
package custom

import (
	"encoding/json"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// InputParameter is the single string parameter carrying the freeform text of
// an emulated custom tool.
const InputParameter = "input"

// FunctionTool renders a freeform custom tool as a plain function tool whose
// only parameter is the freeform text, for backends without a native
// equivalent. The tool keeps its name and description so calls round-trip
// under the name the client registered.
func FunctionTool(t provider.Tool) provider.Tool {
	description := t.Description

	if description == "" {
		description = "Call this tool with a single freeform text input."
	}

	return provider.Tool{
		Name:        t.Name,
		Namespace:   t.Namespace,
		Description: description,

		// defer_loading is a property of the declaration, not of the input
		// shape, so it survives the downgrade unchanged
		Deferred: t.Deferred,

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				InputParameter: map[string]any{
					"type":        "string",
					"description": inputDescription(t),
				},
			},
			"required": []string{InputParameter},
		},
	}
}

// inputDescription tells the model what shape the freeform text must take.
// A grammar-constrained tool cannot be enforced through a JSON schema, so the
// grammar is carried in prose instead — the emulation is best-effort where the
// native tool would have been exact.
func inputDescription(t provider.Tool) string {
	base := "The complete input for this tool, as raw text."

	if t.Format == nil {
		return base
	}

	if t.Format.Type == "grammar" && t.Format.Definition != "" {
		syntax := t.Format.Syntax

		if syntax == "" {
			syntax = "grammar"
		}

		return base + " It must conform to the following " + syntax + ":\n\n" + t.Format.Definition
	}

	return base
}

// IsEmulated reports whether name refers to a tool the caller registered as a
// freeform custom tool. Names are matched after flattening, so it accepts the
// namespaced names backends put on the wire.
func IsEmulated(tools []provider.Tool, name string) bool {
	if name == "" {
		return false
	}

	for _, t := range provider.FlattenTools(tools) {
		if t.Kind == provider.ToolKindCustom && t.Name == name {
			return true
		}
	}

	return false
}

// Wrap encodes freeform text as the emulated function tool's JSON arguments.
func Wrap(input string) string {
	data, err := json.Marshal(map[string]string{InputParameter: input})

	if err != nil {
		return "{}"
	}

	return string(data)
}

// Unwrap decodes emulated function tool arguments back to freeform text.
// Arguments that are not the emulated wrapper are returned unchanged: a
// backend running the tool natively already produces freeform text, and a
// model that ignored the schema is better served by its raw output than by an
// empty string.
func Unwrap(args string) string {
	input, ok := unwrap(args)

	if !ok {
		return args
	}

	return input
}

// IsWrapped reports whether args carry the emulated wrapper shape, and is the
// signal that a tool call needs unwrapping before it reaches a client that
// registered the tool as freeform.
func IsWrapped(args string) bool {
	_, ok := unwrap(args)
	return ok
}

// UnwrapPartial recovers the freeform text from a wrapper the model never
// finished writing, for a stream that ended mid-call. A truncated wrapper does
// not parse, so Unwrap would hand it back verbatim and leak `{"input":"...`
// into a client that registered the tool as freeform; this decodes as much of
// the string value as arrived instead.
//
// ok is false when nothing recoverable is present, in which case callers must
// emit nothing rather than pass the raw wrapper on.
func UnwrapPartial(args string) (string, bool) {
	if input, ok := unwrap(args); ok {
		return input, true
	}

	body, ok := partialInputBody(args)

	if !ok {
		return "", false
	}

	// Drop a trailing incomplete escape ("\", "\u", "\u12") so the body can be
	// closed into a decodable JSON string.
	for len(body) > 0 && !decodableStringBody(body) {
		body = body[:len(body)-1]
	}

	if body == "" {
		return "", false
	}

	var input string

	if err := json.Unmarshal([]byte(`"`+body+`"`), &input); err != nil {
		return "", false
	}

	return input, true
}

func decodableStringBody(body string) bool {
	var out string
	return json.Unmarshal([]byte(`"`+body+`"`), &out) == nil
}

// partialInputBody returns the raw (still JSON-escaped) bytes of the input
// value from a wrapper prefix such as `{"input":"print(`.
func partialInputBody(args string) (string, bool) {
	rest := strings.TrimSpace(args)

	if !strings.HasPrefix(rest, "{") {
		return "", false
	}

	rest = strings.TrimSpace(rest[1:])

	key := `"` + InputParameter + `"`

	if !strings.HasPrefix(rest, key) {
		return "", false
	}

	rest = strings.TrimSpace(rest[len(key):])

	if !strings.HasPrefix(rest, ":") {
		return "", false
	}

	rest = strings.TrimSpace(rest[1:])

	if !strings.HasPrefix(rest, `"`) {
		return "", false
	}

	return rest[1:], true
}

// unwrap accepts only an object whose sole key is the input parameter. A
// broader match would swallow a genuine JSON payload that happens to carry an
// "input" field.
func unwrap(args string) (string, bool) {
	if len(args) == 0 || args[0] != '{' {
		return "", false
	}

	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(args), &fields); err != nil {
		return "", false
	}

	if len(fields) != 1 {
		return "", false
	}

	raw, ok := fields[InputParameter]

	if !ok {
		return "", false
	}

	var input string

	if err := json.Unmarshal(raw, &input); err != nil {
		return "", false
	}

	return input, true
}
