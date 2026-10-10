package mistral

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/extractor"
)

func TestExtractDocumentAndImageRequests(t *testing.T) {
	for _, tt := range []struct {
		name, contentType, mediaType string
	}{
		{"report.PDF", "", "application/pdf"},
		{"report.docx", "", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"slides.pptx", "", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{"image.png", "", "image/png"},
		{"image.jpg", "", "image/jpeg"},
		{"image.jpeg", "", "image/jpeg"},
		{"image.avif", "", "image/avif"},
		{"", "IMAGE/PNG; charset=binary", "image/png"},
		{"report.bin", "application/pdf", "application/pdf"},
	} {
		t.Run(tt.name+tt.contentType, func(t *testing.T) {
			file := extractor.File{Name: tt.name, ContentType: tt.contentType, Content: []byte("input content")}
			calls := 0
			client, err := New(WithToken("key"), WithModel("configured-ocr-model"), WithClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodPost || req.URL.String() != "https://api.mistral.ai/v1/ocr" || req.Header.Get("Authorization") != "Bearer key" {
					t.Errorf("unexpected request: %s %s, headers %v", req.Method, req.URL, req.Header)
				}
				var body struct {
					Model    string            `json:"model"`
					Document map[string]string `json:"document"`
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				kind, key := "document_url", "document_url"
				if strings.HasPrefix(tt.mediaType, "image/") {
					kind, key = "image_url", "image_url"
				}
				wantURL := "data:" + tt.mediaType + ";base64," + base64.StdEncoding.EncodeToString(file.Content)
				if body.Model != "configured-ocr-model" || body.Document["type"] != kind || body.Document[key] != wantURL {
					t.Errorf("OCR payload = %+v", body)
				}
				if kind == "document_url" && body.Document["document_name"] != file.Name {
					t.Errorf("document name = %q, want %q", body.Document["document_name"], file.Name)
				}
				if kind == "image_url" && len(body.Document) != 2 {
					t.Errorf("image payload has document fields: %v", body.Document)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"pages":[{"index":0,"markdown":"extracted text"}]}`))}, nil
			})}))
			if err != nil {
				t.Fatal(err)
			}
			if !client.Capabilities().MaySupport(file) {
				t.Fatal("advertised capabilities exclude a supported request")
			}
			result, err := client.Extract(context.Background(), file, nil)
			if err != nil || result.Text != "extracted text" || calls != 1 {
				t.Fatalf("result = %+v, error = %v, calls = %d", result, err, calls)
			}
		})
	}
}

func TestExtractUnsupportedDoesNotCallAPI(t *testing.T) {
	client, err := New(WithClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsupported file reached the API")
		return nil, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Extract(context.Background(), extractor.File{Name: "table.xlsx", Content: []byte("input")}, nil)
	if !errors.Is(err, extractor.ErrUnsupported) {
		t.Fatalf("error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
