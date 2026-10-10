package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/translator"
	"github.com/adrianliechti/wingman/pkg/translator/google"

	"cloud.google.com/go/auth"
	"google.golang.org/api/googleapi"
)

func TestTranslateOAuthText(t *testing.T) {
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
				if r.Method != http.MethodPost || r.URL.Path != "/v3/projects/test-project/locations/us-central1:translateText" {
					t.Errorf("text request = %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer text-token" || r.Header.Get("X-Goog-User-Project") != "test-project" {
					t.Error("missing text OAuth or billing project header")
				}
				if r.Header.Get("X-Goog-Api-Key") != "" || r.URL.Query().Has("key") {
					t.Error("OAuth text requests must not contain an API key")
				}
				var body struct {
					Parent   string   `json:"parent"`
					Contents []string `json:"contents"`
					Type     string   `json:"mimeType"`
					Target   string   `json:"targetLanguageCode"`
					Source   string   `json:"sourceLanguageCode"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode text request: %v", err)
				}
				if body.Parent != "projects/test-project/locations/us-central1" || len(body.Contents) != 1 || body.Contents[0] != "Hello <world> & friends &amp;" || body.Type != "text/plain" || body.Target != tt.language || body.Source != "" {
					t.Errorf("text request body = %+v", body)
				}
				io.WriteString(w, `{"translations":[{"translatedText":"Hallo <Welt> & Freunde's &amp;","detectedLanguageCode":"en"}]}`)
			}))
			client, err := google.New("https://translate.example/", google.WithProject("test-project"), google.WithLocation("us-central1"), google.WithClient(httpClient),
				google.WithTokenProvider(tokenProviderFunc(func(ctx context.Context) (*auth.Token, error) {
					return &auth.Token{Value: "text-token"}, nil
				})))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			result, err := client.Translate(context.Background(), translator.Input{Text: "  Hello <world> & friends &amp;\n"}, tt.options)
			if err != nil {
				t.Fatal(err)
			}
			if string(result.Content) != "Hallo <Welt> & Freunde's &amp;" || result.ContentType != "text/plain" {
				t.Errorf("translation = %+v", result)
			}
		})
	}
}

func TestTranslateOAuthTextErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "API error", status: http.StatusForbidden, body: `{"error":{"code":403,"message":"Permission denied","status":"PERMISSION_DENIED"}}`, want: "Permission denied"},
		{name: "missing translations", status: http.StatusOK, body: `{}`, want: "unable to translate content"},
		{name: "empty translations", status: http.StatusOK, body: `{"translations":[]}`, want: "unable to translate content"},
		{name: "missing text", status: http.StatusOK, body: `{"translations":[{}]}`, want: "unable to translate content"},
		{name: "multiple translations", status: http.StatusOK, body: `{"translations":[{"translatedText":"a"},{"translatedText":"b"}]}`, want: "unable to translate content"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			httpClient := handlerClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			}))
			client, err := google.New("https://translate.example", google.WithClient(httpClient), google.WithProject("test-project"), google.WithTokenProvider(tokenProviderFunc(func(ctx context.Context) (*auth.Token, error) {
				return &auth.Token{Value: "text-token"}, nil
			})))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			result, err := client.Translate(context.Background(), translator.Input{Text: "Hello world"}, nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) || result != nil {
				t.Fatalf("Translate = %+v, %v; want error containing %q", result, err, tt.want)
			}
			if tt.status != http.StatusOK {
				var apiError *googleapi.Error
				if !errors.As(err, &apiError) || apiError.Code != tt.status {
					t.Errorf("API error lost its HTTP status: %v", err)
				}
			}
		})
	}
}

func TestSDKConcurrentTranslations(t *testing.T) {
	var tokenRequests, translationRequests atomic.Int32
	client, err := google.New("", google.WithProject("test-project"), google.WithTokenProvider(tokenProviderFunc(func(ctx context.Context) (*auth.Token, error) {
		tokenRequests.Add(1)
		return &auth.Token{Value: "cached-token", Expiry: time.Now().Add(time.Hour)}, nil
	})), google.WithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		translationRequests.Add(1)
		if r.Header.Get("Authorization") != "Bearer cached-token" {
			t.Error("missing cached OAuth token")
		}
		body := `{"translations":[{"translatedText":"Hallo Welt"}]}`
		if strings.HasSuffix(r.URL.Path, ":translateDocument") {
			body = `{"documentTranslation":{"byteStreamOutputs":["SGFsbG8gV2VsdA=="],"mimeType":"application/pdf"}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			input := translator.Input{Text: "Hello world"}
			if i%2 == 0 {
				input = translator.Input{File: &translator.File{Name: "file.pdf", Content: []byte("%PDF-input")}}
			}
			result, err := client.Translate(context.Background(), input, &translator.TranslateOptions{Language: "de"})
			if err != nil {
				t.Errorf("concurrent translation: %v", err)
				return
			}
			if string(result.Content) != "Hallo Welt" {
				t.Errorf("concurrent translation result = %q", result.Content)
			}
		})
	}
	wg.Wait()
	if tokenRequests.Load() != 1 || translationRequests.Load() != 12 {
		t.Errorf("OAuth requests = %d, translation requests = %d; want 1 and 12", tokenRequests.Load(), translationRequests.Load())
	}

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Translate(context.Background(), translator.Input{Text: "Hello world"}, nil); err == nil || !strings.Contains(err.Error(), "client is closed") {
		t.Errorf("translation after closing client = %v", err)
	}
}

func TestSDKPreservesHTTPClientTimeout(t *testing.T) {
	client, err := google.New("", google.WithProject("test-project"), google.WithTokenProvider(tokenProviderFunc(func(ctx context.Context) (*auth.Token, error) {
		return &auth.Token{Value: "text-token"}, nil
	})), google.WithClient(&http.Client{Timeout: 30 * time.Millisecond, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > time.Second {
			t.Error("configured HTTP client timeout was lost")
			return nil, errors.New("missing HTTP client timeout")
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Translate(context.Background(), translator.Input{Text: "Hello world"}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("translation timeout error = %v, want context.DeadlineExceeded", err)
	}
}
