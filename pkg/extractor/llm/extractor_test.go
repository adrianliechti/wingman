package llm

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/provider"
)

type recordingCompleter struct{ messages []provider.Message }

func (c *recordingCompleter) Complete(_ context.Context, messages []provider.Message, _ *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	c.messages = messages
	return func(yield func(*provider.Completion, error) bool) {
		message := provider.AssistantMessage("Extracted text")
		yield(&provider.Completion{Message: &message}, nil)
	}
}

func TestExtractNormalizesSupportedFileMetadata(t *testing.T) {
	for _, tt := range []struct {
		name, mediaType, want string
	}{
		{"", "Application/PDF; charset=binary", "application/pdf"},
		{"", " Image/PNG; charset=binary ", "image/png"},
		{"scan.JPEG", "application/octet-stream", "image/jpeg"},
	} {
		t.Run(tt.want, func(t *testing.T) {
			input := extractor.File{Name: tt.name, ContentType: tt.mediaType, Content: []byte("file contents")}
			original := input
			completer := &recordingCompleter{}
			e := New(completer)
			if !e.Capabilities().MaySupport(input) {
				t.Fatal("supported metadata was excluded from routing")
			}
			result, err := e.Extract(t.Context(), input, nil)
			if err != nil || result == nil || result.Text != "Extracted text" {
				t.Fatalf("result %+v, error %v", result, err)
			}
			if len(completer.messages) != 2 || len(completer.messages[1].Content) != 1 {
				t.Fatalf("unexpected messages: %+v", completer.messages)
			}
			file := completer.messages[1].Content[0].File
			if file == nil || file.ContentType != tt.want || file.Name != input.Name || !reflect.DeepEqual(file.Content, input.Content) {
				t.Fatalf("unexpected model input: %+v", file)
			}
			if !reflect.DeepEqual(input, original) {
				t.Fatal("caller file metadata was mutated")
			}
		})
	}
}

func TestExtractRejectsUnsupportedFileBeforeModelCall(t *testing.T) {
	completer := &recordingCompleter{}
	e := New(completer)
	input := extractor.File{Name: "data.csv", ContentType: "text/csv"}
	if e.Capabilities().MaySupport(input) {
		t.Fatal("unsupported file was eligible for routing")
	}
	if _, err := e.Extract(t.Context(), input, nil); !errors.Is(err, extractor.ErrUnsupported) || completer.messages != nil {
		t.Fatalf("error %v, model input %+v", err, completer.messages)
	}
}
