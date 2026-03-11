package tool

import (
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// IsTextEditorTool returns true if the tool type is an Anthropic text editor
// tool (e.g. "text_editor_20250429").
func IsTextEditorTool(toolType string) bool {
	return strings.HasPrefix(toolType, "text_editor_")
}

// IsApplyPatchTool returns true if the tool type is an OpenAI apply-patch tool
// (e.g. "apply_patch_20250605").
func IsApplyPatchTool(toolType string) bool {
	return toolType == "apply_patch" || strings.HasPrefix(toolType, "apply_patch_")
}

// TextEditorTool returns a generic function tool definition that mirrors the
// Anthropic text_editor built-in. The caller supplies the tool name and type
// sent by the client.
func TextEditorTool(name, toolType string, maxCharacters *int) provider.Tool {
	if name == "" {
		name = "str_replace_based_edit_tool"
	}

	commands := []any{"view", "create", "str_replace", "insert"}
	description := "Tool for viewing, creating and editing files.\n\nCommands:\n* `view`: View the content of a file or list a directory. Use `view_range` to view specific line ranges.\n* `create`: Create a new file with specified content. Fails if file exists.\n* `str_replace`: Replace exact text in a file. The `old_str` must match exactly. Use for targeted edits.\n* `insert`: Insert text after a specific line number. Use `insert_line: 0` to insert at the beginning."

	if supportsUndoEdit(toolType) {
		commands = append(commands, "undo_edit")
		description += "\n* `undo_edit`: Undo the last edit made to a file."
	}

	if maxCharacters != nil && *maxCharacters > 0 {
		description += fmt.Sprintf("\n\nWhen using `view`, truncate file content to at most %d characters before returning the tool result.", *maxCharacters)
	}

	return provider.Tool{
		Name:        name,
		Description: description,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The command to run.",
					"enum":        commands,
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute path to file or directory.",
				},
				"file_text": map[string]any{
					"type":        "string",
					"description": "Required for `create` command. The content of the file to create.",
				},
				"old_str": map[string]any{
					"type":        "string",
					"description": "Required for `str_replace` command. The exact string to replace.",
				},
				"new_str": map[string]any{
					"type":        "string",
					"description": "Optional for `str_replace` command. The new string to replace with. Defaults to empty string for deletions.",
				},
				"insert_line": map[string]any{
					"type":        "integer",
					"description": "Required for `insert` command. The line number after which to insert. Use 0 for beginning of file.",
				},
				"insert_text": map[string]any{
					"type":        "string",
					"description": "Required for `insert` command. The text to insert.",
				},
				"view_range": map[string]any{
					"type":        "array",
					"description": "Optional for `view` command. Specifies a line range [start_line, end_line], 1-indexed.",
					"minItems":    2,
					"maxItems":    2,
					"items": map[string]any{
						"type": "integer",
					},
				},
			},
			"required": []any{"command", "path"},
		},
	}
}

func supportsUndoEdit(toolType string) bool {
	if toolType == "" {
		return false
	}

	return toolType == "text_editor_20250124" || toolType == "text_editor_20241022"
}

// ApplyPatchTool returns a generic function tool definition that mirrors the
// OpenAI apply_patch built-in.
func ApplyPatchTool() provider.Tool {
	return provider.Tool{
		Name:        "apply_patch",
		Description: "Apply a patch operation to files. The operation must be one of `create_file`, `update_file`, or `delete_file`. Paths must be workspace-relative, never absolute. For `create_file` and `update_file`, `operation.diff` must use V4A diff hunks only for that single file: use `@@` hunks with context lines, prefer 3 lines of surrounding context, and use additional `@@` context headers when needed to disambiguate repeated code. Do not include a multi-file patch envelope like `*** Begin Patch` / `*** End Patch`, and do not include file action headers inside `operation.diff` because `operation.type` and `operation.path` already specify those.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operation": map[string]any{
					"type":        "object",
					"description": "The apply_patch file operation to execute for a single workspace-relative path.",
					"properties": map[string]any{
						"type": map[string]any{
							"type":        "string",
							"description": "The file operation to perform.",
							"enum":        []any{"create_file", "update_file", "delete_file"},
						},
						"path": map[string]any{
							"type":        "string",
							"description": "The workspace-relative path to create, update, or delete. Never use an absolute path.",
						},
						"diff": map[string]any{
							"type":        "string",
							"description": "Required for `create_file` and `update_file`. The single-file V4A diff body for this operation. Use `@@` hunks with context lines; do not include `*** Begin Patch`, `*** End Patch`, or file action headers.",
						},
					},
					"required": []any{"type", "path"},
				},
			},
			"required": []any{"operation"},
		},
	}
}
