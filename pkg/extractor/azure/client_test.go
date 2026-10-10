package azure

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/extractor"
)

func TestExtractHTMLAndPollingCancellation(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "cancel pending operation"}[pending], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var bodies []*trackedBody
			client, err := New("https://azure.test", WithToken("key"), WithClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				for _, body := range bodies {
					if !body.closed {
						t.Error("previous response body remained open")
					}
				}
				if req.Header.Get("Ocp-Apim-Subscription-Key") != "key" {
					t.Error("missing subscription key")
				}
				status, data := http.StatusOK, `{"status":"succeeded","analyzeResult":{"content":"extracted text"}}`
				header := make(http.Header)
				if req.Method == http.MethodPost {
					if req.URL.Query().Get("api-version") != "2024-11-30" || req.URL.Query().Get("outputContentFormat") != "markdown" || !strings.Contains(req.URL.Path, "prebuilt-layout:analyze") {
						t.Errorf("unexpected analyze URL %s", req.URL)
					}
					input, err := io.ReadAll(req.Body)
					if err != nil || string(input) != "<html>input</html>" {
						t.Error("invalid analyze input")
					}
					status, data = http.StatusAccepted, ""
					header.Set("Operation-Location", "https://azure.test/operation")
				} else if pending {
					cancel()
					data = `{"status":"running"}`
				}
				body := &trackedBody{Reader: strings.NewReader(data)}
				bodies = append(bodies, body)
				return &http.Response{StatusCode: status, Header: header, Body: body}, nil
			})}))
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			result, err := client.Extract(ctx, extractor.File{Name: "page.html", Content: []byte("<html>input</html>")}, nil)
			if pending {
				if !errors.Is(err, context.Canceled) || time.Since(start) > time.Second {
					t.Fatalf("cancellation error = %v", err)
				}
			} else if err != nil || result.Text != "extracted text" {
				t.Fatalf("result = %+v, error = %v", result, err)
			}
			if len(bodies) != 2 {
				t.Errorf("requests = %d", len(bodies))
			}
			for _, body := range bodies {
				if !body.closed {
					t.Error("response body remained open")
				}
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }
