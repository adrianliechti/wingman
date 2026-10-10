package multi

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/extractor/plain"
	"github.com/adrianliechti/wingman/pkg/otel"
)

type fakeExtractor struct {
	calls   int
	result  *extractor.Document
	err     error
	execute func()
}

func (f *fakeExtractor) Capabilities() extractor.Capabilities {
	return extractor.Capabilities{UnknownFormats: true}
}

func (f *fakeExtractor) Extract(context.Context, extractor.File, *extractor.ExtractOptions) (*extractor.Document, error) {
	f.calls++
	if f.execute != nil {
		f.execute()
	}
	return f.result, f.err
}

type formatExtractor struct {
	*fakeExtractor
	capability extractor.Capabilities
}

func (f *formatExtractor) Capabilities() extractor.Capabilities { return f.capability }

func TestExtract_RoutesByCapabilitiesAndPreservesProviderOrder(t *testing.T) {
	pdf := &formatExtractor{fakeExtractor: &fakeExtractor{result: &extractor.Document{Text: "wrong"}}, capability: extractor.Capabilities{Extensions: []string{".pdf"}}}
	csv := &formatExtractor{fakeExtractor: &fakeExtractor{result: &extractor.Document{Text: "table"}}, capability: extractor.Capabilities{Extensions: []string{".csv"}}}
	providers := []extractor.Provider{nil, pdf, csv}
	e := New(providers...)
	providers[2] = nil
	got, err := e.Extract(t.Context(), extractor.File{Name: "data.csv"}, nil)
	if err != nil || got.Text != "table" || pdf.calls != 0 || csv.calls != 1 {
		t.Fatalf("got %+v, err %v, calls %d/%d", got, err, pdf.calls, csv.calls)
	}
	capability := e.Capabilities()
	if !reflect.DeepEqual(capability.Extensions, []string{".csv", ".pdf"}) {
		t.Fatalf("combined capabilities %+v", capability)
	}
	capability.Extensions[0] = "changed"
	if e.Capabilities().Extensions[0] != ".csv" {
		t.Fatal("aggregate capabilities mutated the provider")
	}
}

func TestExtract_FallbackAndFailureSemantics(t *testing.T) {
	failed := errors.New("upstream unavailable")
	for _, test := range []struct {
		first, second *fakeExtractor
		want          error
		success       bool
	}{
		{&fakeExtractor{err: failed}, &fakeExtractor{result: &extractor.Document{Text: "fallback"}}, nil, true},
		{&fakeExtractor{err: failed}, &fakeExtractor{err: extractor.ErrUnsupported}, failed, false},
		{&fakeExtractor{err: fmt.Errorf("format: %w", extractor.ErrUnsupported)}, &fakeExtractor{err: extractor.ErrUnsupported}, extractor.ErrUnsupported, false},
		{&fakeExtractor{}, &fakeExtractor{result: &extractor.Document{Text: "fallback"}}, nil, true},
	} {
		e := New(test.first, test.second)
		got, err := e.Extract(t.Context(), extractor.File{Name: "unknown"}, nil)
		if test.success {
			if err != nil || got == nil || got.Text != "fallback" {
				t.Fatalf("fallback got %+v, err %v", got, err)
			}
		} else if !errors.Is(err, test.want) {
			t.Fatalf("error %v, want %v", err, test.want)
		}
		if test.first.calls != 1 || test.second.calls != 1 {
			t.Fatal("providers with unknown formats were skipped")
		}
		if !e.Capabilities().UnknownFormats {
			t.Fatal("unknown provider restrictions were invented")
		}
	}
	if _, err := New(&fakeExtractor{}).Extract(t.Context(), extractor.File{}, nil); err == nil || errors.Is(err, extractor.ErrUnsupported) {
		t.Fatalf("nil result error %v", err)
	}
}

func TestExtract_CancellationStopsFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	first := &fakeExtractor{execute: cancel, err: extractor.ErrUnsupported}
	second := &fakeExtractor{result: &extractor.Document{Text: "should not run"}}
	if _, err := New(first, second).Extract(ctx, extractor.File{}, nil); !errors.Is(err, context.Canceled) || second.calls != 0 {
		t.Fatalf("cancellation err %v, fallback calls %d", err, second.calls)
	}
	if _, err := New().Extract(ctx, extractor.File{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("empty chain cancellation %v", err)
	}
	if _, err := New(&fakeExtractor{err: context.DeadlineExceeded}, second).Extract(t.Context(), extractor.File{}, nil); !errors.Is(err, context.DeadlineExceeded) || second.calls != 0 {
		t.Fatalf("deadline err %v, fallback calls %d", err, second.calls)
	}
}

func TestExtract_ExplicitUnsupportedProviderIsSkipped(t *testing.T) {
	unsupported := &formatExtractor{fakeExtractor: &fakeExtractor{result: &extractor.Document{Text: "wrong"}}}
	unknown := &fakeExtractor{result: &extractor.Document{Text: "remote"}}
	e := New(unsupported, unknown)
	got, err := e.Extract(t.Context(), extractor.File{Name: "unknown.bin"}, nil)
	if err != nil || got == nil || got.Text != "remote" || unsupported.calls != 0 || unknown.calls != 1 {
		t.Fatalf("got %+v, error %v, calls %d/%d", got, err, unsupported.calls, unknown.calls)
	}
	if caps := e.Capabilities(); !caps.UnknownFormats {
		t.Fatalf("unknown format support was lost: %+v", caps)
	}
}

func TestExtractUnlistedTextThroughTracingAndMulti(t *testing.T) {
	p, err := plain.New()
	if err != nil {
		t.Fatal(err)
	}
	e := New(otel.NewExtractor("plain", "text", p))
	file := extractor.File{Name: "payload.bin", ContentType: "application/octet-stream", Content: []byte("Readable text")}
	result, err := e.Extract(t.Context(), file, nil)
	if err != nil || result == nil || result.Text != "Readable text" {
		t.Fatalf("content detection: result %+v, error %v", result, err)
	}
	file.Content = []byte{0, 1, 2, 3}
	if _, err := e.Extract(t.Context(), file, nil); !errors.Is(err, extractor.ErrUnsupported) {
		t.Fatalf("binary file error %v, want ErrUnsupported", err)
	}
}
