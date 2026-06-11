// Package texteditor bridges the two model-native file-editing tool dialects:
// OpenAI's apply_patch (create/update/delete operations carrying V4A diffs)
// and Anthropic's text editor (view/create/str_replace/insert commands).
//
// A text-editor tool keeps the dialect of the client that registered it
// (provider.Tool.Name). Backends with a native tool of the same dialect use
// it directly; all other backends emulate the tool as a plain function tool
// in the client's dialect (see FunctionTool), so tool calls and results
// round-trip without lossy diff/str_replace conversion. The Operation/Input
// converters cover the remaining cross-dialect cases: replaying mixed
// histories and Codex's freeform patch envelope.
package texteditor

import (
	"encoding/json"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

const (
	// NameApplyPatch is the OpenAI apply_patch dialect ({type, path, diff}).
	NameApplyPatch = "apply_patch"

	// NameTextEditor is the Anthropic text editor dialect ({command, path, ...}).
	NameTextEditor = "str_replace_based_edit_tool"
)

// Operation is an apply_patch operation.
type Operation struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Diff string `json:"diff,omitempty"`
}

func ParseOperation(args string) Operation {
	var op Operation
	json.Unmarshal([]byte(args), &op)
	return op
}

func (op Operation) Args() string {
	data, _ := json.Marshal(map[string]any{
		"type": op.Type,
		"path": op.Path,
		"diff": op.Diff,
	})
	return string(data)
}

// Input is a text editor command input.
type Input struct {
	Command string `json:"command"`
	Path    string `json:"path"`

	FileText string `json:"file_text,omitempty"`

	OldStr string `json:"old_str,omitempty"`
	NewStr string `json:"new_str,omitempty"`

	InsertLine *int   `json:"insert_line,omitempty"`
	InsertText string `json:"insert_text,omitempty"`

	ViewRange []int `json:"view_range,omitempty"`
}

func ParseInput(args string) Input {
	var input Input
	json.Unmarshal([]byte(args), &input)
	return input
}

func (in Input) Args() string {
	data, _ := json.Marshal(in)
	return string(data)
}

func (in Input) Map() map[string]any {
	data, _ := json.Marshal(in)

	result := map[string]any{}
	json.Unmarshal(data, &result)

	return result
}

// Input converts an apply_patch operation to the closest text editor command.
// delete_file has no text editor equivalent and degrades to a view of the path.
func (op Operation) Input() Input {
	switch op.Type {
	case "create_file":
		return Input{
			Command:  "create",
			Path:     op.Path,
			FileText: diffAddedLines(op.Diff),
		}

	case "update_file":
		oldStr, newStr := splitDiff(op.Diff)
		return Input{
			Command: "str_replace",
			Path:    op.Path,
			OldStr:  oldStr,
			NewStr:  newStr,
		}

	default:
		return Input{
			Command: "view",
			Path:    op.Path,
		}
	}
}

// Operation converts a text editor command to the closest apply_patch
// operation. view has no apply_patch equivalent and degrades to an empty
// update_file; insert loses its line anchor (the diff carries added lines only).
func (in Input) Operation() Operation {
	switch in.Command {
	case "create":
		return Operation{
			Type: "create_file",
			Path: in.Path,
			Diff: addedDiff(in.FileText),
		}

	case "str_replace":
		return Operation{
			Type: "update_file",
			Path: in.Path,
			Diff: replaceDiff(in.OldStr, in.NewStr),
		}

	case "insert":
		return Operation{
			Type: "update_file",
			Path: in.Path,
			Diff: addedDiff(in.InsertText),
		}

	default:
		return Operation{
			Type: "update_file",
			Path: in.Path,
		}
	}
}

// diffAddedLines extracts the file contents of a create_file diff (every line
// prefixed with "+").
func diffAddedLines(diff string) string {
	var lines []string

	for line := range strings.Lines(diff) {
		line = strings.TrimSuffix(line, "\n")

		if rest, ok := strings.CutPrefix(line, "+"); ok {
			lines = append(lines, rest)
		}
	}

	return strings.Join(lines, "\n")
}

// splitDiff converts a V4A update diff into the old/new pair of a str_replace.
// Context lines (" "-prefixed or bare) are kept on both sides so the old text
// matches the file uniquely. Hunks separated by "@@" markers are concatenated —
// a best-effort approximation, since one str_replace cannot span disjoint
// regions of a file.
func splitDiff(diff string) (string, string) {
	var oldLines, newLines []string

	for line := range strings.Lines(diff) {
		line = strings.TrimSuffix(line, "\n")

		switch {
		case strings.HasPrefix(line, "@@"):
			// hunk marker, optionally with a location anchor — not file content

		case strings.HasPrefix(line, "-"):
			oldLines = append(oldLines, line[1:])

		case strings.HasPrefix(line, "+"):
			newLines = append(newLines, line[1:])

		case strings.HasPrefix(line, " "):
			oldLines = append(oldLines, line[1:])
			newLines = append(newLines, line[1:])

		default:
			oldLines = append(oldLines, line)
			newLines = append(newLines, line)
		}
	}

	return strings.Join(oldLines, "\n"), strings.Join(newLines, "\n")
}

func addedDiff(content string) string {
	if content == "" {
		return ""
	}

	var b strings.Builder

	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		b.WriteString("+" + line + "\n")
	}

	return b.String()
}

func replaceDiff(oldText, newText string) string {
	var b strings.Builder
	b.WriteString("@@\n")

	for _, line := range strings.Split(strings.TrimRight(oldText, "\n"), "\n") {
		b.WriteString("-" + line + "\n")
	}

	for _, line := range strings.Split(strings.TrimRight(newText, "\n"), "\n") {
		b.WriteString("+" + line + "\n")
	}

	return b.String()
}

// Envelope renders the operation as the raw patch envelope Codex's freeform
// apply_patch tool expects as the custom_tool_call input.
func (op Operation) Envelope() string {
	var b strings.Builder
	b.WriteString("*** Begin Patch\n")

	switch op.Type {
	case "create_file":
		b.WriteString("*** Add File: " + op.Path + "\n")
	case "delete_file":
		b.WriteString("*** Delete File: " + op.Path + "\n")
	default:
		b.WriteString("*** Update File: " + op.Path + "\n")
	}

	if op.Diff != "" {
		b.WriteString(op.Diff)
		if !strings.HasSuffix(op.Diff, "\n") {
			b.WriteString("\n")
		}
	}

	b.WriteString("*** End Patch\n")
	return b.String()
}

// ParseEnvelope is the inverse of Envelope: it extracts the operation type,
// path, and diff body from a raw patch envelope produced by Codex.
func ParseEnvelope(input string) Operation {
	var op Operation
	var body []string

	for _, line := range strings.Split(input, "\n") {
		switch {
		case strings.HasPrefix(line, "*** Begin Patch"):
			continue
		case strings.HasPrefix(line, "*** End Patch"):
			continue
		case strings.HasPrefix(line, "*** Add File: "):
			op.Type = "create_file"
			op.Path = strings.TrimPrefix(line, "*** Add File: ")
		case strings.HasPrefix(line, "*** Update File: "):
			op.Type = "update_file"
			op.Path = strings.TrimPrefix(line, "*** Update File: ")
		case strings.HasPrefix(line, "*** Delete File: "):
			op.Type = "delete_file"
			op.Path = strings.TrimPrefix(line, "*** Delete File: ")
		case strings.HasPrefix(line, "*** Move to: "):
			continue
		default:
			body = append(body, line)
		}
	}

	op.Diff = strings.TrimRight(strings.Join(body, "\n"), "\n")
	if op.Diff != "" {
		op.Diff += "\n"
	}
	return op
}

// FunctionTool renders a text-editor tool as a plain function tool in the same
// dialect, for backends without a native equivalent. Keeping the client's
// dialect end-to-end means calls and results need no conversion.
func FunctionTool(t provider.Tool) provider.Tool {
	if t.Name == NameApplyPatch {
		return provider.Tool{
			Name:        NameApplyPatch,
			Description: applyPatchDescription,
			Parameters:  applyPatchSchema(),
		}
	}

	return provider.Tool{
		Name:        NameTextEditor,
		Description: textEditorDescription,
		Parameters:  textEditorSchema(),
	}
}

const applyPatchDescription = `Create, update, or delete files in the workspace. ` +
	`Updates are expressed as V4A diffs: one or more hunks, each introduced by a line containing "@@" ` +
	`(optionally followed by the enclosing function or class as a locator), ` +
	`where removed lines start with "-", added lines with "+", and unchanged context lines with a single space. ` +
	`Include about 3 unchanged context lines around each change so the hunk applies unambiguously.`

func applyPatchSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type": map[string]any{
				"type":        "string",
				"enum":        []string{"create_file", "update_file", "delete_file"},
				"description": "The operation to perform.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Path of the file, relative to the workspace root.",
			},
			"diff": map[string]any{
				"type": "string",
				"description": "For create_file: the full contents of the new file, every line prefixed with \"+\". " +
					"For update_file: a V4A diff of the changes. Omit for delete_file.",
			},
		},
		"required": []string{"type", "path"},
	}
}

const textEditorDescription = `View and edit text files in the workspace. ` +
	`Use "view" to read a file (optionally a line range) or list a directory, "create" to write a new file, ` +
	`"str_replace" to replace text that occurs exactly once in a file, and "insert" to add text after a given line. ` +
	`For str_replace, old_str must match the file contents exactly (including whitespace) at exactly one location; ` +
	`include enough surrounding lines to make it unique.`

func textEditorSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"enum":        []string{"view", "create", "str_replace", "insert"},
				"description": "The command to run.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file or directory.",
			},
			"view_range": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "integer"},
				"description": "Optional [start, end] line range for view (1-indexed, -1 for end of file).",
			},
			"file_text": map[string]any{
				"type":        "string",
				"description": "Contents of the new file for create.",
			},
			"old_str": map[string]any{
				"type":        "string",
				"description": "Exact text to replace for str_replace.",
			},
			"new_str": map[string]any{
				"type":        "string",
				"description": "Replacement text for str_replace.",
			},
			"insert_line": map[string]any{
				"type":        "integer",
				"description": "Line number after which to insert text (0 for beginning of file).",
			},
			"insert_text": map[string]any{
				"type":        "string",
				"description": "Text to insert for insert.",
			},
		},
		"required": []string{"command", "path"},
	}
}
