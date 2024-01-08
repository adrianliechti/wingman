package react

import (
	_ "embed"

	"github.com/adrianliechti/llama/pkg/prompt"
)

var (
	//go:embed prompt.tmpl
	promptTemplateText string
	promptTemplate     = prompt.MustNew(promptTemplateText)

	promptStop = []string{
		"\n###",
		"\nObservation:",
	}
)

type promptData struct {
	Input string

	Messages  []promptMessage
	Functions []promptFunction
}

type promptMessage struct {
	Type    string
	Content string
}

type promptFunction struct {
	Name        string
	Description string
}
