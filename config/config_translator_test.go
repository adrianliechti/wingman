package config

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/translator"
)

func TestGoogleTranslatorConfiguration(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-google-token")

	previousClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: googleTranslatorRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		if r.URL.Host != "configured-translator.test" || r.URL.Path != "/language/translate/v2" || r.Header.Get("X-Goog-Api-Key") != "test-google-token" {
			t.Error("Google translator URL or token was not configured")
		}
		var body struct {
			Target string `json:"target"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Target != "de" {
			t.Errorf("target language = %q, want de", body.Target)
		}
		io.WriteString(w, `{"data":{"translations":[{"translatedText":"Hallo Welt"}]}}`)
		return w.Result(), nil
	})}
	t.Cleanup(func() { http.DefaultClient = previousClient })

	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
translators:
  google:
    type: google
    url: https://configured-translator.test
    token: ${GOOGLE_API_KEY}
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse(path)
	if err != nil {
		t.Fatalf("parse Google translator configuration: %v", err)
	}

	for _, id := range []string{"google", ""} {
		p, err := cfg.Translator(id)
		if err != nil {
			t.Fatalf("translator %q: %v", id, err)
		}
		result, err := p.Translate(context.Background(), translator.Input{Text: "Hello world"}, &translator.TranslateOptions{Language: "de"})
		if err != nil {
			t.Fatal(err)
		}
		if string(result.Content) != "Hallo Welt" || result.ContentType != "text/plain" {
			t.Errorf("translator %q result = %+v", id, result)
		}
	}
}

func TestGoogleTranslatorDocumentConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	data := []byte(`{"type":"authorized_user","client_id":"test-client","client_secret":"test-secret","refresh_token":"test-refresh-token"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "detected-project")

	for _, tt := range []struct {
		name     string
		vars     map[string]string
		project  string
		location string
	}{
		{name: "configured project and region", vars: map[string]string{"project": "configured-project", "location": "us-central1"}, project: "configured-project", location: "us-central1"},
		{name: "ADC project and default region", project: "detected-project", location: "global"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tokenRequests := 0
			documentRequests := 0
			client := &http.Client{Transport: googleTranslatorRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := ""
				if r.URL.Host == "oauth2.googleapis.com" {
					tokenRequests++
					if err := r.ParseForm(); err != nil {
						t.Errorf("parse OAuth request: %v", err)
					}
					if r.Form.Get("refresh_token") != "test-refresh-token" {
						t.Error("ADC refresh token was not loaded")
					}
					body = `{"access_token":"adc-token","token_type":"Bearer","expires_in":3600}`
				} else {
					documentRequests++
					wantPath := "/v3/projects/" + tt.project + "/locations/" + tt.location + ":translateDocument"
					if r.URL.Path != wantPath || r.Header.Get("Authorization") != "Bearer adc-token" || r.Header.Get("X-Goog-User-Project") != tt.project {
						t.Errorf("document configuration was not applied: %s", r.URL)
					}
					var request struct {
						Target   string `json:"targetLanguageCode"`
						Native   bool   `json:"isTranslateNativePdfOnly"`
						Rotation bool   `json:"enableRotationCorrection"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Errorf("decode document request: %v", err)
					}
					if request.Target != "en" || request.Native || !request.Rotation {
						t.Errorf("document request = %+v", request)
					}
					body = `{"documentTranslation":{"byteStreamOutputs":["JVBERi10cmFuc2xhdGVk"]}}`
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			})}

			p, err := createTranslator(translatorConfig{Type: "google", Token: "text-api-key", Vars: tt.vars}, translatorContext{Client: client})
			if err != nil {
				t.Fatal(err)
			}
			if tokenRequests != 0 {
				t.Error("translator construction must not request OAuth tokens")
			}
			for range 2 {
				result, err := p.Translate(context.Background(), translator.Input{File: &translator.File{Name: "file.pdf", Content: []byte("%PDF-input")}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				if string(result.Content) != "%PDF-translated" || result.ContentType != "application/pdf" || result.Name != "file.pdf" {
					t.Errorf("document result = %+v", result)
				}
			}
			if tokenRequests != 1 || documentRequests != 2 {
				t.Errorf("OAuth requests = %d, document requests = %d; want 1 and 2", tokenRequests, documentRequests)
			}
		})
	}
}

type googleTranslatorRoundTripFunc func(*http.Request) (*http.Response, error)

func (f googleTranslatorRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
