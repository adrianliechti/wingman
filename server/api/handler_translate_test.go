package api

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/translator"
	"github.com/adrianliechti/wingman/pkg/translator/llm"
)

type translatePolicy struct{}

func (translatePolicy) Verify(context.Context, policy.Resource, string, policy.Action) error {
	return nil
}

type recordingTranslator struct {
	caps     translator.Capabilities
	input    translator.Input
	language string
	calls    int
	result   *translator.File
	err      error
}

func (p *recordingTranslator) Capabilities() translator.Capabilities { return p.caps }

func (p *recordingTranslator) Translate(_ context.Context, input translator.Input, options *translator.TranslateOptions) (*translator.File, error) {
	p.calls++
	p.input, p.language = input, options.Language
	return p.result, p.err
}

type recordingTranslateExtractor struct {
	calls int
	err   error
}

func (*recordingTranslateExtractor) Capabilities() extractor.Capabilities {
	return extractor.Capabilities{UnknownFormats: true}
}

func (p *recordingTranslateExtractor) Extract(_ context.Context, file extractor.File, _ *extractor.ExtractOptions) (*extractor.Document, error) {
	p.calls++
	return &extractor.Document{Text: "extracted text"}, p.err
}

func translationUpload(t *testing.T, accept string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("language", "de"); err != nil {
		t.Fatal(err)
	}
	part, err := form.CreateFormFile("file", "document.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("%PDF-input")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/translate", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	return r
}

func translationHandler(p translator.Provider) *Handler {
	cfg := &config.Config{Policy: translatePolicy{}}
	cfg.RegisterTranslator("", otel.NewTranslator("test", "", p))
	return New(cfg)
}

func TestTranslateDocumentPreservesFileInput(t *testing.T) {
	p := &recordingTranslator{
		caps:   translator.Capabilities{FileToDocument: translator.Supported},
		result: &translator.File{Content: []byte("%PDF-output"), ContentType: "application/pdf"},
	}
	w := httptest.NewRecorder()
	translationHandler(p).handleTranslate(w, translationUpload(t, "application/pdf"))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/pdf" || w.Body.String() != "%PDF-output" {
		t.Fatalf("document response = %d %s", w.Code, w.Body.String())
	}
	if p.calls != 1 || p.input.File == nil || p.input.File.Name != "document.pdf" || string(p.input.File.Content) != "%PDF-input" || p.input.Text != "" || p.language != "de" {
		t.Fatalf("document input = %+v, language %q, calls %d", p.input, p.language, p.calls)
	}
}

func TestTranslateRejectsUnsupportedModesBeforeCallingProvider(t *testing.T) {
	for _, tt := range []struct {
		name    string
		caps    translator.Capabilities
		accept  string
		status  int
		message string
	}{
		{"text-only provider", translator.Capabilities{TextToText: translator.Supported}, "application/pdf", http.StatusBadRequest, "file input"},
		{"file input with text output", translator.Capabilities{TextToText: translator.Supported, FileToText: translator.Supported}, "application/pdf", http.StatusNotAcceptable, "Accept: text/plain"},
		{"document-only provider", translator.Capabilities{FileToDocument: translator.Supported}, "text/plain", http.StatusBadRequest, "text output"},
		{"no modes", translator.Capabilities{}, "text/plain", http.StatusBadRequest, "text output"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := &recordingTranslator{caps: tt.caps}
			w := httptest.NewRecorder()
			translationHandler(p).handleTranslate(w, translationUpload(t, tt.accept))
			if w.Code != tt.status || !strings.Contains(w.Body.String(), tt.message) || p.calls != 0 {
				t.Fatalf("response = %d %s, calls %d", w.Code, w.Body.String(), p.calls)
			}
		})
	}
	// The real LLM adapter can read attachments but cannot generate a PDF.
	w := httptest.NewRecorder()
	translationHandler(llm.New(nil)).handleTranslate(w, translationUpload(t, "application/pdf"))
	if w.Code != http.StatusNotAcceptable {
		t.Fatalf("LLM document request = %d %s", w.Code, w.Body.String())
	}
}

func TestTranslateTextFromFileUsesExtraction(t *testing.T) {
	for _, accept := range []string{"", "*/*", "text/plain"} {
		p := &recordingTranslator{caps: translator.Capabilities{TextToText: translator.Supported}, result: &translator.File{Content: []byte("übersetzt"), ContentType: "text/plain"}}
		extraction := &recordingTranslateExtractor{}
		h := translationHandler(p)
		h.RegisterExtractor("", extraction)
		w := httptest.NewRecorder()
		h.handleTranslate(w, translationUpload(t, accept))
		if w.Code != http.StatusOK || w.Body.String() != "übersetzt" || extraction.calls != 1 || p.calls != 1 || p.input.Text != "extracted text" || p.input.File != nil {
			t.Fatalf("Accept %q: response %d %s, input %+v, extract calls %d, translate calls %d", accept, w.Code, w.Body.String(), p.input, extraction.calls, p.calls)
		}
	}
}

func TestTranslateHandlesFailedAndMissingResponses(t *testing.T) {
	for _, tt := range []struct {
		err     error
		status  int
		message string
	}{
		{errors.New("provider failed"), http.StatusBadRequest, "provider failed"},
		{nil, http.StatusBadGateway, "empty response"},
	} {
		p := &recordingTranslator{caps: translator.Capabilities{FileToDocument: translator.Supported}, err: tt.err}
		w := httptest.NewRecorder()
		translationHandler(p).handleTranslate(w, translationUpload(t, "application/pdf"))
		if w.Code != tt.status || !strings.Contains(w.Body.String(), tt.message) || p.calls != 1 {
			t.Fatalf("response %d %s, calls %d", w.Code, w.Body.String(), p.calls)
		}
	}
}

func TestTranslateNativeFileToTextRouting(t *testing.T) {
	for _, caps := range []translator.Capabilities{
		{FileToText: translator.Supported},
		{TextToText: translator.Supported, FileToText: translator.Supported},
		{FileToText: translator.Unknown},
	} {
		for _, accept := range []string{"", "*/*", "text/plain"} {
			p := &recordingTranslator{caps: caps, result: &translator.File{Content: []byte("übersetzt"), ContentType: "text/plain"}}
			extraction := &recordingTranslateExtractor{}
			h := translationHandler(p)
			h.RegisterExtractor("", extraction)
			w := httptest.NewRecorder()
			h.handleTranslate(w, translationUpload(t, accept))
			if w.Code != http.StatusOK || w.Body.String() != "übersetzt" || extraction.calls != 0 || p.calls != 1 || p.input.File == nil || string(p.input.File.Content) != "%PDF-input" || p.input.Text != "" {
				t.Fatalf("caps %+v, Accept %q: response %d %s, input %+v, extract calls %d, translate calls %d", caps, accept, w.Code, w.Body.String(), p.input, extraction.calls, p.calls)
			}
		}
	}
}

func TestTranslateUnknownModesUseExtractionForText(t *testing.T) {
	p := &recordingTranslator{
		caps:   translator.Capabilities{TextToText: translator.Unknown, FileToText: translator.Unknown, FileToDocument: translator.Unknown},
		result: &translator.File{Content: []byte("übersetzt"), ContentType: "text/plain"},
	}
	extraction := &recordingTranslateExtractor{}
	h := translationHandler(p)
	h.RegisterExtractor("", extraction)
	w := httptest.NewRecorder()
	h.handleTranslate(w, translationUpload(t, "text/plain"))
	if w.Code != http.StatusOK || extraction.calls != 1 || p.calls != 1 || p.input.Text != "extracted text" || p.input.File != nil {
		t.Fatalf("response %d %s, input %+v, extract calls %d", w.Code, w.Body.String(), p.input, extraction.calls)
	}
}

func TestTranslateExplicitTextTakesPrecedenceOverUpload(t *testing.T) {
	for _, mode := range []translator.Support{translator.Supported, translator.Unsupported} {
		p := &recordingTranslator{
			caps:   translator.Capabilities{TextToText: mode, FileToText: translator.Supported},
			result: &translator.File{Content: []byte("übersetzt"), ContentType: "text/plain"},
		}
		extraction := &recordingTranslateExtractor{}
		h := translationHandler(p)
		h.RegisterExtractor("", extraction)
		r := translationUpload(t, "text/plain")
		r.URL.RawQuery = "input=Hello+world"
		w := httptest.NewRecorder()
		h.handleTranslate(w, r)
		if mode == translator.Supported {
			if w.Code != http.StatusOK || p.calls != 1 || p.input.Text != "Hello world" || p.input.File != nil {
				t.Fatalf("response %d %s, input %+v, calls %d", w.Code, w.Body.String(), p.input, p.calls)
			}
		} else if w.Code != http.StatusBadRequest || p.calls != 0 || !strings.Contains(w.Body.String(), "text input") {
			t.Fatalf("unsupported text input: response %d %s, calls %d", w.Code, w.Body.String(), p.calls)
		}
		if extraction.calls != 0 {
			t.Fatal("explicit text was sent through extraction")
		}
	}
}

func TestTranslateExtractionFailureStopsTranslation(t *testing.T) {
	p := &recordingTranslator{caps: translator.Capabilities{TextToText: translator.Supported}}
	h := translationHandler(p)
	h.RegisterExtractor("", &recordingTranslateExtractor{err: errors.New("extraction failed")})
	w := httptest.NewRecorder()
	h.handleTranslate(w, translationUpload(t, "text/plain"))
	if w.Code != http.StatusBadRequest || p.calls != 0 || !strings.Contains(w.Body.String(), "extraction failed") {
		t.Fatalf("response %d %s, calls %d", w.Code, w.Body.String(), p.calls)
	}
}
