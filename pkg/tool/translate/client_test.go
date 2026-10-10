package translate

import (
	"context"
	"errors"
	"testing"

	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/translator"
)

type fakeTranslator struct {
	input    translator.Input
	language string
	result   *translator.File
	err      error
}

type fileOnlyTranslator struct{ fakeTranslator }

func (*fakeTranslator) Capabilities() translator.Capabilities {
	return translator.Capabilities{TextToText: translator.Supported}
}

func (*fileOnlyTranslator) Capabilities() translator.Capabilities {
	return translator.Capabilities{FileToDocument: translator.Supported}
}

func TestNewRejectsFileOnlyProvider(t *testing.T) {
	p := &fileOnlyTranslator{}
	for _, candidate := range []translator.Provider{p, otel.NewTranslator("custom", "files", p)} {
		if _, err := New(candidate); !errors.Is(err, translator.ErrUnsupported) {
			t.Fatalf("text tool accepted a file-only provider: %v", err)
		}
	}
}

func (f *fakeTranslator) Translate(_ context.Context, input translator.Input, options *translator.TranslateOptions) (*translator.File, error) {
	f.input, f.language = input, options.Language
	return f.result, f.err
}

func TestExecute_PreservesVerbatimTextAndLanguage(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("accepted missing provider")
	}
	for _, lang := range []string{"de", "pt-BR", "zh-Hant", "sr-Latn-RS"} {
		f := &fakeTranslator{result: &translator.File{Content: []byte("  Übersetzung\n")}}
		c, _ := New(f)
		text := "  Original\ntext with markup <b>here</b>\n"
		got, err := c.Execute(t.Context(), ToolName, map[string]any{"text": text, "lang": " " + lang + " "})
		if err != nil || got != string(f.result.Content) || f.input.Text != text || f.language != lang {
			t.Fatalf("text %q, language %q, got %v, err %v", f.input.Text, f.language, got, err)
		}
		if c.Result(ToolName, got).Parts[0].Text != got {
			t.Fatal("translation was quoted or trimmed")
		}
	}
}

func TestExecute_RejectsInvalidInputsAndEmptyResponse(t *testing.T) {
	f := &fakeTranslator{}
	c, _ := New(f)
	for _, params := range []map[string]any{
		nil, {"text": " ", "lang": "de"}, {"text": 1, "lang": "de"}, {"text": "x", "lang": 42},
		{"text": "x", "lang": ""}, {"text": "x", "lang": "translate to German"}, {"text": "x", "lang": "en-!!!!"},
	} {
		if _, err := c.Execute(t.Context(), ToolName, params); err == nil || f.input.Text != "" {
			t.Fatalf("invalid input reached provider: %v, err %v", params, err)
		}
	}
	if _, err := c.Execute(t.Context(), "wrong", map[string]any{"text": "x", "lang": "de"}); err == nil {
		t.Fatal("accepted wrong tool")
	}
	if _, err := c.Execute(t.Context(), ToolName, map[string]any{"text": "x", "lang": "de"}); err == nil {
		t.Fatal("accepted nil response")
	}
	f.err = errors.New("provider failed")
	if _, err := c.Execute(t.Context(), ToolName, map[string]any{"text": "x", "lang": "de"}); !errors.Is(err, f.err) {
		t.Fatalf("lost provider error: %v", err)
	}
}
