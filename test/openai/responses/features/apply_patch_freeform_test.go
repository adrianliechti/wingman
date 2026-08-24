package features_test

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/responses"
)

// applyPatchGrammar is the Lark grammar Codex declares for its freeform
// apply_patch tool. The grammar allows multiple file hunks per envelope.
const applyPatchGrammar = `start: begin_patch hunk+ end_patch
begin_patch: "*** Begin Patch" LF
end_patch: "*** End Patch" LF?

hunk: add_hunk | delete_hunk | update_hunk
add_hunk: "*** Add File: " filename LF add_line+
delete_hunk: "*** Delete File: " filename LF
update_hunk: "*** Update File: " filename LF change_move? change?

filename: /(.+)/
add_line: "+" /(.*)/ LF -> line

change_move: "*** Move to: " filename LF
change: (change_context | change_line)+ eof_line?
change_context: ("@@" | "@@ " /(.+)/) LF
change_line: ("+" | "-" | " ") /(.*)/ LF
eof_line: "*** End of File" LF

%import common.LF
`

const multiFilePatchEnvelope = "*** Begin Patch\n" +
	"*** Add File: hello.py\n" +
	"+print(\"hello\")\n" +
	"*** Update File: goodbye.py\n" +
	"@@\n" +
	"-print(\"bye\")\n" +
	"+print(\"goodbye\")\n" +
	"*** End Patch\n"

// TestApplyPatchFreeformMultiFileHTTP replays a Codex-style conversation with
// a freeform apply_patch tool whose prior call carries a multi-file envelope.
// The comparison leg proves the real API accepts the replay; the wingman leg
// proves the envelope survives the gateway verbatim.
func TestApplyPatchFreeformMultiFileHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.TextEditor || !strings.HasPrefix(strings.ToLower(model.Name), "gpt-") {
			// the freeform passthrough targets OpenAI upstreams
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": []map[string]any{
					{
						"type": "message",
						"role": "user",
						"content": []map[string]any{
							{"type": "input_text", "text": "Create hello.py and fix goodbye.py, then summarize what you changed in one sentence."},
						},
					},
					{
						"type":    "custom_tool_call",
						"call_id": "call_patch_multi_e2e",
						"name":    "apply_patch",
						"input":   multiFilePatchEnvelope,
					},
					{
						"type":    "custom_tool_call_output",
						"call_id": "call_patch_multi_e2e",
						"output":  "Done. Applied 2 file changes.",
					},
				},
				"tools": []map[string]any{
					{
						"type":        "custom",
						"name":        "apply_patch",
						"description": "The `apply_patch` tool can be used to edit files. This is a FREEFORM tool, so do not wrap the patch in JSON.",
						"format": map[string]any{
							"type":       "grammar",
							"syntax":     "lark",
							"definition": applyPatchGrammar,
						},
					},
				},
			}

			openaiResp, wingmanResp := responses.CompareHTTP(t, h, model, body)

			responses.RequireMessageOutput(t, "openai", openaiResp.Body)
			responses.RequireMessageOutput(t, "wingman", wingmanResp.Body)
		})
	}
}
