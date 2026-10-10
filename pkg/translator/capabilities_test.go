package translator_test

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/tool/translate"
	"github.com/adrianliechti/wingman/pkg/translator"
	"github.com/adrianliechti/wingman/pkg/translator/azure"
	"github.com/adrianliechti/wingman/pkg/translator/custom"
	"github.com/adrianliechti/wingman/pkg/translator/deepl"
	"github.com/adrianliechti/wingman/pkg/translator/google"
	"github.com/adrianliechti/wingman/pkg/translator/llm"
)

type unknownTranslator struct{}

func (unknownTranslator) Capabilities() translator.Capabilities {
	return translator.Capabilities{TextToText: translator.Unknown, FileToText: translator.Unknown, FileToDocument: translator.Unknown}
}

func (unknownTranslator) Translate(_ context.Context, input translator.Input, _ *translator.TranslateOptions) (*translator.File, error) {
	return &translator.File{Content: []byte(input.Text), ContentType: "text/plain"}, nil
}

func TestUnknownProviderRemainsUsableThroughTracing(t *testing.T) {
	for _, p := range []translator.Provider{unknownTranslator{}, otel.NewTranslator("custom", "unknown", unknownTranslator{})} {
		c, err := translate.New(p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Execute(t.Context(), translate.ToolName, map[string]any{"text": "hello", "lang": "en"})
		if err != nil || got != "hello" {
			t.Fatalf("unknown provider: got %v, error %v", got, err)
		}
		if p.Capabilities() != (unknownTranslator{}).Capabilities() {
			t.Fatal("unknown support changed through tracing")
		}
	}
}

func TestBuiltinModesAndTracing(t *testing.T) {
	for _, tt := range []struct {
		provider translator.Provider
		want     translator.Capabilities
	}{
		{&azure.Client{}, translator.Capabilities{TextToText: translator.Supported, FileToDocument: translator.Supported}},
		{&deepl.Client{}, translator.Capabilities{TextToText: translator.Supported, FileToDocument: translator.Supported}},
		{&google.Client{}, translator.Capabilities{TextToText: translator.Supported, FileToDocument: translator.Supported}},
		{llm.New(nil), translator.Capabilities{TextToText: translator.Supported, FileToText: translator.Supported}},
		{&custom.Client{}, (unknownTranslator{}).Capabilities()},
	} {
		for _, p := range []translator.Provider{tt.provider, otel.NewTranslator("test", "", tt.provider)} {
			if got := p.Capabilities(); got != tt.want {
				t.Fatalf("%T modes = %+v, want %+v", p, got, tt.want)
			}
		}
	}
}

func TestSupportStates(t *testing.T) {
	for _, tt := range []struct {
		support translator.Support
		want    bool
	}{
		{translator.Unsupported, false}, {translator.Supported, true}, {translator.Unknown, true}, {translator.Support(255), false},
	} {
		if got := tt.support.MaySupport(); got != tt.want {
			t.Fatalf("support %v: may support %v, want %v", tt.support, got, tt.want)
		}
	}
}
