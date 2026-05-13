package codex

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// convertMessages walks the conversation and produces:
//   - the joined system text (every system-role message)
//   - the user inputs to send via turn/start (final user message)
//   - any tool result the caller is answering (must be the last message)
//
// codex doesn't take prior assistant turns inline — they're carried by the
// thread store on the server side; we re-attach via thread/resume.
func convertMessages(messages []provider.Message) (system string, inputs []userInput, tempImages []string, ok bool) {
	if len(messages) == 0 || messages[len(messages)-1].Role != provider.MessageRoleUser {
		return "", nil, nil, false
	}

	var systemParts []string
	for _, m := range messages {
		if m.Role == provider.MessageRoleSystem {
			if t := strings.TrimSpace(m.Text()); t != "" {
				systemParts = append(systemParts, t)
			}
		}
	}

	last := messages[len(messages)-1]
	inputs, tempImages = userInputsFromMessage(last)
	if len(inputs) == 0 {
		return "", nil, nil, false
	}

	return strings.Join(systemParts, "\n\n"), inputs, tempImages, true
}

// userInputsFromMessage produces the codex UserInput[] for a single user
// message. Image File contents are spilled to temp files because codex's
// LocalImage variant takes a path, not bytes; tempImages must be removed by
// the caller after the turn completes.
//
// Tool results are folded in as text — codex's dynamic-tool-call protocol
// expects the answer inline to a server request, but the wingman provider
// abstraction surfaces the tool call across iterator boundaries. By the time
// the caller invokes Complete() with a ToolResult, the original codex turn
// has already finished. We splice the result into a follow-up user message
// so the model still sees it.
func userInputsFromMessage(m provider.Message) ([]userInput, []string) {
	var (
		inputs     []userInput
		tempImages []string

		textParts []string
	)

	for _, c := range m.Content {
		switch {
		case c.ToolResult != nil:
			textParts = append(textParts, fmt.Sprintf(
				"[tool %s result]\n%s",
				c.ToolResult.ID, c.ToolResult.Text(),
			))

			// Image parts in the tool result spill as localImage inputs so the
			// model still sees them — codex has no structured tool result that
			// can carry images inline.
			for _, p := range c.ToolResult.Parts {
				if p.File == nil {
					continue
				}
				switch p.File.ContentType {
				case "image/jpeg", "image/png", "image/gif", "image/webp":
					if path, err := writeTempImage(p.File); err == nil {
						inputs = append(inputs, userInput{Type: "localImage", Path: path})
						tempImages = append(tempImages, path)
					} else {
						data := base64.StdEncoding.EncodeToString(p.File.Content)
						inputs = append(inputs, userInput{
							Type: "image",
							URL:  "data:" + p.File.ContentType + ";base64," + data,
						})
					}
				}
			}

		case c.File != nil:
			switch c.File.ContentType {
			case "image/jpeg", "image/png", "image/gif", "image/webp":
				if path, err := writeTempImage(c.File); err == nil {
					inputs = append(inputs, userInput{Type: "localImage", Path: path})
					tempImages = append(tempImages, path)
				} else {
					// fall back to embedding as a data URL
					data := base64.StdEncoding.EncodeToString(c.File.Content)
					inputs = append(inputs, userInput{
						Type: "image",
						URL:  "data:" + c.File.ContentType + ";base64," + data,
					})
				}
			}

		case c.Text != "":
			textParts = append(textParts, c.Text)
		}
	}

	if len(textParts) > 0 {
		inputs = append([]userInput{{Type: "text", Text: strings.Join(textParts, "\n\n")}}, inputs...)
	}

	return inputs, tempImages
}

func writeTempImage(f *provider.File) (string, error) {
	suffix := extForMime(f.ContentType)
	tmp, err := os.CreateTemp("", "wingman-codex-img-*"+suffix)
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(f.Content); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

func extForMime(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	return ""
}
