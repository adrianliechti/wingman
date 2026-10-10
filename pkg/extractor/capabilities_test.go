package extractor_test

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/extractor/azure"
	"github.com/adrianliechti/wingman/pkg/extractor/docling"
	"github.com/adrianliechti/wingman/pkg/extractor/kreuzberg"
	"github.com/adrianliechti/wingman/pkg/extractor/llm"
	"github.com/adrianliechti/wingman/pkg/extractor/mistral"
	"github.com/adrianliechti/wingman/pkg/extractor/plain"
	"github.com/adrianliechti/wingman/pkg/otel"
)

func TestCapabilities_FormatMatching(t *testing.T) {
	capability := extractor.Capabilities{MediaTypes: []string{"application/pdf", "image/*"}, Extensions: []string{".pdf"}}
	for _, test := range []struct {
		file extractor.File
		want bool
	}{
		{extractor.File{Name: "REPORT.PDF"}, true},
		{extractor.File{ContentType: "Application/PDF; charset=binary"}, true},
		{extractor.File{ContentType: "image/png"}, true},
		{extractor.File{Name: "report.txt", ContentType: "text/plain"}, false},
		{extractor.File{ContentType: "imagebad/png"}, false},
		{extractor.File{}, false},
	} {
		if got := capability.MaySupport(test.file); got != test.want {
			t.Fatalf("file %+v: accepts %v", test.file, got)
		}
	}
	if !(extractor.Capabilities{UnknownFormats: true}).MaySupport(extractor.File{Name: "unknown.bin"}) {
		t.Fatal("unknown service was ruled out")
	}
	if (extractor.Capabilities{}).MaySupport(extractor.File{Name: "report.pdf", ContentType: "application/pdf"}) {
		t.Fatal("empty capabilities advertised support")
	}
	if !(extractor.Capabilities{MediaTypes: []string{"Application/PDF"}, Extensions: []string{".PDF"}}).MaySupport(extractor.File{Name: "report.pdf"}) {
		t.Fatal("configured extension matching was case sensitive")
	}
}

func TestBuiltinCapabilities_AndTracingWrapper(t *testing.T) {
	for _, provider := range []extractor.Provider{&azure.Client{}, &docling.Client{}, &kreuzberg.Client{}, &mistral.Client{}, llm.New(nil)} {
		capability := provider.Capabilities()
		if !capability.MaySupport(extractor.File{Name: "report.pdf"}) || capability.MaySupport(extractor.File{Name: "binary.unknown"}) {
			t.Fatalf("incorrect capabilities for %T: %+v", provider, capability)
		}
		if len(capability.MediaTypes) == 0 {
			t.Fatalf("missing formats for %T", provider)
		}
		original := capability.MediaTypes[0]
		capability.MediaTypes[0] = "changed"
		if provider.Capabilities().MediaTypes[0] != original {
			t.Fatalf("capability mutation changed %T", provider)
		}
	}
	wrapped := otel.NewExtractor("mistral", "ocr", &mistral.Client{})
	if wrapped.Capabilities().MaySupport(extractor.File{Name: "table.xlsx"}) {
		t.Fatal("wrapper lost Mistral's format restrictions")
	}
	if !wrapped.Capabilities().MaySupport(extractor.File{Name: "image.png"}) {
		t.Fatal("wrapper lost Mistral's image support")
	}
	p, _ := plain.New()
	if !p.Capabilities().MaySupport(extractor.File{Name: "unknown.bin", ContentType: "application/octet-stream"}) {
		t.Fatal("plain text sniffing was ruled out by metadata")
	}
}
