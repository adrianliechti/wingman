package server_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json/jsontext"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestCompressedAPIRoutes(t *testing.T) {
	for _, route := range []struct {
		name, path, body string
	}{
		{"systemone", "/v1/systemone", `{"model":"chat","state":"hello","questions":{"q":{"type":"noul"}}}`},
		{"decisions", "/v1/decisions", `{"model":"chat","input":"hello","questions":[{"type":"predicate","instructions":"Urgent?"}]}`},
		{"chat", "/v1/chat/completions", `{"model":"chat","messages":[{"role":"user","content":"hello"}]}`},
		{"responses", "/v1/responses", `{"model":"chat","input":"hello"}`},
		{"anthropic", "/v1/messages", `{"model":"chat","max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`},
		{"gemini", "/v1beta/models/chat:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`},
	} {
		for _, encoding := range []string{"", "gzip", "x-gzip", "zstd"} {
			t.Run(route.name+"/"+encoding, func(t *testing.T) {
				backend := &decisionCompleter{output: `{"q0":{"0":0.25,"1":0.75}}`}
				_, s := decisionsServer(t, backend)
				// SDKs start compressing at 1 KiB.
				body := []byte(route.body + strings.Repeat(" ", 2048))
				if encoding != "" {
					var buffer bytes.Buffer
					var writer io.WriteCloser
					if encoding == "zstd" {
						encoder, err := zstd.NewWriter(&buffer, zstd.WithEncoderConcurrency(1))
						if err != nil {
							t.Fatal(err)
						}
						writer = encoder
					} else {
						writer = gzip.NewWriter(&buffer)
					}
					if _, err := writer.Write(body); err != nil {
						t.Fatal(err)
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					body = buffer.Bytes()
				}
				request := httptest.NewRequest(http.MethodPost, route.path, bytes.NewReader(body))
				request.Header.Set("Content-Encoding", encoding)
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				s.ServeHTTP(response, request)
				if response.Code != http.StatusOK || backend.calls != 1 || !jsontext.Value(response.Body.Bytes()).IsValid() {
					t.Fatalf("status %d, calls %d: %s", response.Code, backend.calls, response.Body.String())
				}
			})
		}
	}
}
