package tavily

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestScrapeClosesResponseBody(t *testing.T) {
	for _, tt := range []struct {
		name, response string
		status         int
		wantError      bool
	}{
		{"success", `{"results":[{"raw_content":"extracted text"}]}`, http.StatusOK, false},
		{"HTTP error", `permission denied`, http.StatusForbidden, true},
		{"invalid JSON", `invalid`, http.StatusOK, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := &trackedBody{Reader: strings.NewReader(tt.response)}
			client, err := New("key", WithClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Header.Get("Authorization") != "Bearer key" {
					t.Error("missing token")
				}
				return &http.Response{StatusCode: tt.status, Body: body}, nil
			})}))
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Scrape(context.Background(), "https://source.test", nil)
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v", err)
			}
			if !tt.wantError && result.Text != "extracted text" {
				t.Fatalf("result = %+v", result)
			}
			if !body.closed {
				t.Error("response body not closed")
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
