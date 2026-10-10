package llm

import (
	"context"
	"iter"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/translator"
)

type recordingCompleter struct{ messages []provider.Message }

func (c *recordingCompleter) Complete(_ context.Context, messages []provider.Message, _ *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	c.messages = messages
	return func(yield func(*provider.Completion, error) bool) {
		message := provider.AssistantMessage("Hallo Welt")
		yield(&provider.Completion{Message: &message}, nil)
	}
}

func TestTranslateInputModesReturnText(t *testing.T) {
	file := &translator.File{Name: "document.pdf", ContentType: "application/pdf", Content: []byte("%PDF-input")}
	for _, input := range []translator.Input{{Text: "Hello world"}, {File: file}} {
		c := &recordingCompleter{}
		p := New(c)
		result, err := p.Translate(t.Context(), input, &translator.TranslateOptions{Language: "de"})
		if err != nil || result == nil || string(result.Content) != "Hallo Welt" || result.ContentType != "text/plain" {
			t.Fatalf("translation result = %+v, error %v", result, err)
		}
		if len(c.messages) != 2 || len(c.messages[1].Content) != 1 {
			t.Fatalf("unexpected input messages: %+v", c.messages)
		}
		content := c.messages[1].Content[0]
		if content.Text != input.Text || content.File != input.File {
			t.Fatalf("input changed: %+v", content)
		}
		caps := p.Capabilities()
		if caps.TextToText != translator.Supported || caps.FileToText != translator.Supported || caps.FileToDocument.MaySupport() {
			t.Fatalf("capabilities do not describe the observed behavior: %+v", caps)
		}
	}
}
