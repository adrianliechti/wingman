package claude

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// convertMessages extracts:
//   - the joined system text (every system-role message in messages)
//   - the user frame to write to stdin (the last message, which must be a
//     user message — anything else is an invalid input)
//
// Prior turns are NOT folded into the prompt — they're carried by the CLI's
// own session store via --resume. If the caller hands us a multi-turn
// conversation we have no cached session for, prior context is lost; the
// completer logs and warns.
//
// If the last user message contains tool_result blocks (the caller is
// answering a tool_use from a prior turn), they are rendered as tool_result
// content blocks in the user frame — the CLI input shape the model expects.
func convertMessages(messages []provider.Message) (system string, frame userFrame, ok bool) {
	if len(messages) == 0 || messages[len(messages)-1].Role != provider.MessageRoleUser {
		return "", userFrame{}, false
	}

	var systemParts []string
	for _, m := range messages {
		if m.Role == provider.MessageRoleSystem {
			if t := strings.TrimSpace(m.Text()); t != "" {
				systemParts = append(systemParts, t)
			}
		}
	}

	blocks := blocksForUserMessage(messages[len(messages)-1])
	if len(blocks) == 0 {
		return "", userFrame{}, false
	}

	return strings.Join(systemParts, "\n\n"), userFrame{
		Type:    "user",
		Message: userFrameInner{Role: "user", Content: blocks},
	}, true
}

func blocksForUserMessage(m provider.Message) []cliContent {
	var blocks []cliContent

	for _, c := range m.Content {
		switch {
		case c.ToolResult != nil:
			data := c.ToolResult.Data
			raw, err := json.Marshal(data)
			if err != nil {
				raw = []byte(`""`)
			}
			blocks = append(blocks, cliContent{
				Type:       "tool_result",
				ToolUseID:  c.ToolResult.ID,
				ResultData: raw,
			})

		case c.File != nil:
			data := base64.StdEncoding.EncodeToString(c.File.Content)
			mime := c.File.ContentType

			switch mime {
			case "image/jpeg", "image/png", "image/gif", "image/webp":
				blocks = append(blocks, cliContent{
					Type: "image",
					Source: &cliSource{
						Type:      "base64",
						MediaType: mime,
						Data:      data,
					},
				})

			case "application/pdf":
				blocks = append(blocks, cliContent{
					Type: "document",
					Source: &cliSource{
						Type:      "base64",
						MediaType: mime,
						Data:      data,
					},
				})
			}

		case c.Text != "":
			blocks = append(blocks, cliContent{Type: "text", Text: c.Text})
		}
	}

	return blocks
}
