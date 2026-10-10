package google_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/translator"
	"github.com/adrianliechti/wingman/pkg/translator/google"

	"cloud.google.com/go/auth"
)

func TestTranslateDocuments(t *testing.T) {
	for _, tt := range []struct {
		name        string
		contentType string
		wantType    string
	}{
		{name: "file.pdf", contentType: "application/pdf", wantType: "application/pdf"},
		{name: "file.PDF", wantType: "application/pdf"},
		{name: "file.doc", wantType: "application/msword"},
		{name: "file.DOCX", contentType: "application/octet-stream", wantType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{name: "file.ppt", wantType: "application/vnd.ms-powerpoint"},
		{name: "file.pptx", contentType: "application/zip", wantType: "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{name: "file.xls", wantType: "application/vnd.ms-excel"},
		{name: "file.xlsx", wantType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{name: "no-extension", contentType: "application/pdf; charset=binary", wantType: "application/pdf"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			inputBytes := []byte{0, 255, 1, 128, 'H', 'i'}
			outputBytes := []byte{0, 254, 2, 128, 'H', 'a', 'l', 'l', 'o'}
			httpClient := handlerClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v3/projects/test-project/locations/global:translateDocument" {
					t.Errorf("document request = %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer document-token" || r.Header.Get("X-Goog-User-Project") != "test-project" {
					t.Error("missing document OAuth or billing project header")
				}
				if r.Header.Get("X-Goog-Api-Key") != "" || r.URL.Query().Has("key") {
					t.Error("document requests must use OAuth instead of an API key")
				}
				var body struct {
					Target string `json:"targetLanguageCode"`
					Source string `json:"sourceLanguageCode"`
					Input  struct {
						Content []byte `json:"content"`
						Type    string `json:"mimeType"`
					} `json:"documentInputConfig"`
					NativePDF bool `json:"isTranslateNativePdfOnly"`
					Rotation  bool `json:"enableRotationCorrection"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode document request: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if body.Target != "de" || body.Source != "" || body.Input.Type != tt.wantType || !bytes.Equal(body.Input.Content, inputBytes) {
					t.Errorf("document request body = %+v", body)
				}
				if body.NativePDF {
					t.Error("native-only mode must remain disabled to support scanned PDFs")
				}
				if body.Rotation != (tt.wantType == "application/pdf") {
					t.Errorf("PDF rotation correction = %v", body.Rotation)
				}
				json.NewEncoder(w).Encode(map[string]any{
					"documentTranslation": map[string]any{
						"byteStreamOutputs": [][]byte{outputBytes},
						"mimeType":          tt.wantType,
					},
				})
			}))

			client, err := google.New("https://translate.example", google.WithClient(httpClient), google.WithToken("text-key"), google.WithProject("test-project"),
				google.WithTokenProvider(tokenProviderFunc(func(ctx context.Context) (*auth.Token, error) {
					return &auth.Token{Value: "document-token"}, nil
				})))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			input := &translator.File{Name: tt.name, Content: inputBytes, ContentType: tt.contentType}
			result, err := client.Translate(context.Background(), translator.Input{File: input}, &translator.TranslateOptions{Language: "de"})
			if err != nil {
				t.Fatal(err)
			}
			if result.Name != tt.name || result.ContentType != tt.wantType || !bytes.Equal(result.Content, outputBytes) {
				t.Errorf("translated document = %+v", result)
			}
			if input.Name != tt.name || input.ContentType != tt.contentType || !bytes.Equal(input.Content, inputBytes) {
				t.Error("Translate changed the input file")
			}
		})
	}
}

func TestTranslateDocumentErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "API error", status: http.StatusForbidden, body: `{"error":{"message":"Permission denied"}}`, want: "Permission denied"},
		{name: "missing document", status: http.StatusOK, body: `{}`, want: "expected one translated document"},
		{name: "empty document", status: http.StatusOK, body: `{"documentTranslation":{"byteStreamOutputs":[""]}}`, want: "expected one translated document"},
		{name: "multiple documents", status: http.StatusOK, body: `{"documentTranslation":{"byteStreamOutputs":["YQ==","Yg=="]}}`, want: "expected one translated document"},
		{name: "invalid base64", status: http.StatusOK, body: `{"documentTranslation":{"byteStreamOutputs":["!!!"]}}`, want: "invalid value for bytes field"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			httpClient := handlerClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			}))
			client, err := google.New("https://translate.example", google.WithClient(httpClient), google.WithProject("test-project"), google.WithTokenProvider(tokenProviderFunc(func(ctx context.Context) (*auth.Token, error) {
				return &auth.Token{Value: "document-token"}, nil
			})))
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Translate(context.Background(), translator.Input{File: &translator.File{Name: "file.pdf", Content: []byte("document")}}, nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) || result != nil {
				t.Errorf("Translate = %+v, %v; want error containing %q", result, err, tt.want)
			}
		})
	}
}

func TestTranslateDocumentValidationAndAuthentication(t *testing.T) {
	for _, tt := range []struct {
		name    string
		file    *translator.File
		project string
		token   string
		want    string
	}{
		{name: "empty file", file: &translator.File{Name: "file.pdf"}, want: "no document content"},
		{name: "oversized file", file: &translator.File{Name: "file.pdf", Content: make([]byte, 20*1024*1024+1)}, want: "20 MB limit"},
		{name: "unsupported file", file: &translator.File{Name: "file.txt", Content: []byte("hello")}, want: "unsupported type"},
		{name: "missing project", file: &translator.File{Name: "file.pdf", Content: []byte("document")}, want: "vars.project"},
		{name: "invalid project", file: &translator.File{Name: "file.pdf", Content: []byte("document")}, project: "../other", want: "invalid translation project"},
		{name: "empty token", file: &translator.File{Name: "file.pdf", Content: []byte("document")}, project: "test-project", want: "empty access token"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client, err := google.New("", google.WithProject(tt.project), google.WithTokenProvider(tokenProviderFunc(func(ctx context.Context) (*auth.Token, error) {
				return &auth.Token{Value: tt.token}, nil
			})), google.WithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				t.Error("invalid input or authentication should not send a translation request")
				return nil, errors.New("unexpected request")
			})}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Translate(context.Background(), translator.Input{File: tt.file}, nil); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Translate error = %v, want %q", err, tt.want)
			}
		})
	}

	t.Run("API key without ADC", func(t *testing.T) {
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "missing.json"))
		client, err := google.New("", google.WithToken("text-key"), google.WithProject("test-project"))
		if err != nil {
			t.Fatalf("API-key client should initialize without ADC: %v", err)
		}
		_, err = client.Translate(context.Background(), translator.Input{File: &translator.File{Name: "file.pdf", Content: []byte("document")}}, nil)
		if err == nil || !strings.Contains(err.Error(), "Application Default Credentials") {
			t.Errorf("missing document credentials error = %v", err)
		}
	})

	t.Run("context cancellation during authentication", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		client, err := google.New("", google.WithProject("test-project"), google.WithTokenProvider(tokenProviderFunc(func(ctx context.Context) (*auth.Token, error) {
			return nil, ctx.Err()
		})))
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Translate(ctx, translator.Input{File: &translator.File{Name: "file.pdf", Content: []byte("document")}}, nil)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("authentication error = %v, want context.Canceled", err)
		}
	})
}

type tokenProviderFunc func(context.Context) (*auth.Token, error)

func (f tokenProviderFunc) Token(ctx context.Context) (*auth.Token, error) {
	return f(ctx)
}
