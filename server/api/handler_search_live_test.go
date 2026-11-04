package api_test

import (
	"bytes"
	"encoding/json/v2"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/webtest"
)

func TestLiveNativeSearchText(t *testing.T) {
	for _, backend := range []string{"exa", "tavily"} {
		t.Run(backend, func(t *testing.T) {
			local := webtest.LiveServer(t, backend)
			for _, format := range []string{"json", "multipart"} {
				t.Run(format, func(t *testing.T) {
					var body bytes.Buffer
					media, endpoint := "application/json", "/v1/search"
					if format == "json" {
						body.WriteString(`{"query":"Go programming language documentation","limit":1,"domain":["go.dev"],"text":{}}`)
					} else {
						writer := multipart.NewWriter(&body)
						for name, value := range map[string]string{"input": "Go programming language documentation", "limit": "1", "domain": "go.dev", "text": "true"} {
							if err := writer.WriteField(name, value); err != nil {
								t.Fatal(err)
							}
						}
						if err := writer.Close(); err != nil {
							t.Fatal(err)
						}
						media, endpoint = writer.FormDataContentType(), "/v1/retrieve"
					}
					r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, local.URL+endpoint, &body)
					if err != nil {
						t.Fatal(err)
					}
					r.Header.Set("Content-Type", media)
					r.Header.Set("Authorization", "Bearer "+webtest.LiveKey)
					response, err := local.Client().Do(r)
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					if response.StatusCode != 200 {
						t.Fatalf("HTTP %d", response.StatusCode)
					}
					var hits []map[string]any
					if err := json.UnmarshalRead(response.Body, &hits); err != nil {
						t.Fatal(err)
					}
					if len(hits) != 1 {
						t.Fatal("wrong result count")
					}
					source, err := url.Parse(hits[0]["source"].(string))
					if err != nil || source.Hostname() != "go.dev" {
						t.Fatal("domain filter was lost")
					}
					excerpt, _ := hits[0]["excerpt"].(string)
					text, _ := hits[0]["text"].(string)
					if excerpt == "" || !strings.Contains(text, "Go") {
						t.Fatal("excerpt or requested page text was lost")
					}
					if _, legacy := hits[0]["content"]; legacy {
						t.Fatal("legacy result field returned")
					}
				})
			}
		})
	}
}
