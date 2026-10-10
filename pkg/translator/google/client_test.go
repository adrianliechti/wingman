package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/translator"
	"github.com/adrianliechti/wingman/pkg/translator/google"
)

func TestTranslate(t *testing.T) {
	for _, tt := range []struct {
		name     string
		options  *translator.TranslateOptions
		language string
	}{
		{name: "nil options", language: "en"},
		{name: "default language", options: &translator.TranslateOptions{}, language: "en"},
		{name: "target language", options: &translator.TranslateOptions{Language: "de"}, language: "de"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			httpClient := handlerClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/language/translate/v2" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("X-Goog-Api-Key") != "test-key" {
					t.Error("missing API key header")
				}
				if r.URL.RawQuery != "" {
					t.Errorf("unexpected query parameters: %s", r.URL.RawQuery)
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
				}

				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				var texts []string
				var language, format string
				if err := json.Unmarshal(body["q"], &texts); err != nil {
					t.Errorf("decode input: %v", err)
				}
				if err := json.Unmarshal(body["target"], &language); err != nil {
					t.Errorf("decode target language: %v", err)
				}
				if err := json.Unmarshal(body["format"], &format); err != nil {
					t.Errorf("decode input format: %v", err)
				}
				if len(texts) != 1 || texts[0] != "Hello <world> & friends &amp;" || language != tt.language || format != "text" {
					t.Errorf("request body = %s", body)
				}
				if _, ok := body["source"]; ok {
					t.Error("source language must be detected automatically")
				}

				io.WriteString(w, `{"data":{"translations":[{"translatedText":"Hallo <Welt> & Freunde's &amp;","detectedSourceLanguage":"en"}]}}`)
			}))
			client, err := google.New("https://translate.example/", google.WithToken("test-key"), google.WithClient(httpClient))
			if err != nil {
				t.Fatal(err)
			}
			originalLanguage := ""
			if tt.options != nil {
				originalLanguage = tt.options.Language
			}
			result, err := client.Translate(context.Background(), translator.Input{Text: "  Hello <world> & friends &amp;\n"}, tt.options)
			if err != nil {
				t.Fatal(err)
			}
			if string(result.Content) != "Hallo <Welt> & Freunde's &amp;" || result.ContentType != "text/plain" {
				t.Errorf("translation = %+v", result)
			}
			if tt.options != nil && tt.options.Language != originalLanguage {
				t.Error("Translate changed the caller's options")
			}
		})
	}
}

func TestTranslateErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "API error", status: http.StatusForbidden, body: `{"error":{"message":"API key not valid"}}`, want: "API key not valid"},
		{name: "empty error", status: http.StatusServiceUnavailable, want: "Service Unavailable"},
		{name: "invalid JSON", status: http.StatusOK, body: "invalid", want: "invalid character"},
		{name: "missing translations", status: http.StatusOK, body: `{}`, want: "unable to translate content"},
		{name: "empty translations", status: http.StatusOK, body: `{"data":{"translations":[]}}`, want: "unable to translate content"},
		{name: "missing text", status: http.StatusOK, body: `{"data":{"translations":[{}]}}`, want: "unable to translate content"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			httpClient := handlerClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			}))
			client, err := google.New("https://translate.example", google.WithToken("test-key"), google.WithClient(httpClient))
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Translate(context.Background(), translator.Input{Text: "Hello world"}, nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Translate error = %v, want %q", err, tt.want)
			}
			if result != nil {
				t.Errorf("unexpected result on error: %+v", result)
			}
		})
	}
}

func TestTranslateRejectsInvalidInput(t *testing.T) {
	client, err := google.New("", google.WithToken("test-key"), google.WithClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			t.Error("invalid input should not send a request")
			return nil, errors.New("unexpected request")
		}),
	}))
	if err != nil {
		t.Fatal(err)
	}

	for _, input := range []translator.Input{{}, {Text: " \n\t"}} {
		if _, err := client.Translate(context.Background(), input, nil); err == nil || !strings.Contains(err.Error(), "no content") {
			t.Errorf("empty input error = %v", err)
		}
	}
	if _, err := client.Translate(context.Background(), translator.Input{
		Text: "Hello world",
		File: &translator.File{Name: "document.txt", Content: []byte("content"), ContentType: "text/plain"},
	}, nil); !errors.Is(err, translator.ErrUnsupported) {
		t.Errorf("file input error = %v, want ErrUnsupported", err)
	}
}

func TestTranslateDefaultEndpointAndContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client, err := google.New("", google.WithToken("test-key"), google.WithClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://translate.googleapis.com/language/translate/v2" {
				t.Errorf("default endpoint = %s", r.URL)
			}
			if r.Context().Err() != context.Canceled {
				t.Error("request does not use the caller's context")
			}
			return nil, r.Context().Err()
		}),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Translate(ctx, translator.Input{Text: "Hello world"}, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("Translate error = %v, want context.Canceled", err)
	}
}

func TestTranslateInvalidURL(t *testing.T) {
	for _, endpoint := range []string{"http://%", "://invalid"} {
		client, err := google.New(endpoint, google.WithToken("test-key"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Translate(context.Background(), translator.Input{Text: "Hello world"}, nil); err == nil {
			t.Errorf("Translate with URL %q should fail", endpoint)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// handlerClient exercises request serialization and HTTP response handling
// without requiring a listening socket.
func handlerClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		response := w.Result()
		response.Request = r
		return response, nil
	})}
}
