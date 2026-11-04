package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/auth/static"

	"github.com/klauspost/compress/zstd"
)

func compressedBody(t *testing.T, encoding string, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	var writer io.WriteCloser
	if encoding == "zstd" {
		encoder, err := zstd.NewWriter(&buffer, zstd.WithWindowSize(8<<20), zstd.WithEncoderConcurrency(1))
		if err != nil {
			t.Fatal(err)
		}
		writer = encoder
	} else {
		writer = gzip.NewWriter(&buffer)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestRequestDecompressor(t *testing.T) {
	data := []byte(`{"state":"` + strings.Repeat("state", 400) + `"}`)
	for _, encoding := range []string{"", "identity", "gzip", "x-gzip", "zstd", " GZip "} {
		t.Run(encoding, func(t *testing.T) {
			body := data
			if encoding != "" && encoding != "identity" {
				body = compressedBody(t, encoding, data)
			}
			request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
			request.Header.Set("Content-Encoding", encoding)
			request.Header.Set("Content-Type", "multipart/form-data; boundary=example")
			calls := 0
			handler := handleRequestDecompression(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				decoded, err := io.ReadAll(r.Body)
				if err != nil || !bytes.Equal(decoded, data) || r.Header.Get("Content-Type") != "multipart/form-data; boundary=example" {
					t.Errorf("invalid decoded request: %v, headers %v", err, r.Header)
				}
				if encoding != "" && encoding != "identity" && (r.ContentLength != -1 || r.Header.Get("Content-Length") != "" || r.Header.Get("Content-Encoding") != "") {
					t.Errorf("encoded metadata reached handler: %v", r.Header)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent || calls != 1 {
				t.Fatalf("status %d, calls %d: %s", response.Code, calls, response.Body.String())
			}
		})
	}
}

func TestRequestDecompressorRejectsInvalidBodies(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 2048)
	gzipBody := compressedBody(t, "gzip", data)
	badChecksum := bytes.Clone(gzipBody)
	badChecksum[len(badChecksum)-8] ^= 0xff
	zstdBody := compressedBody(t, "zstd", data)
	for _, tc := range []struct {
		name     string
		encoding []string
		body     []byte
		status   int
	}{
		{"unsupported", []string{"br"}, data, 415},
		{"stacked", []string{"gzip, zstd"}, gzipBody, 415},
		{"multiple headers", []string{"gzip", "zstd"}, gzipBody, 415},
		{"invalid gzip", []string{"gzip"}, []byte("bad"), 400},
		{"invalid zstd", []string{"zstd"}, []byte("bad"), 400},
		{"truncated gzip", []string{"gzip"}, gzipBody[:len(gzipBody)-1], 400},
		{"gzip checksum", []string{"gzip"}, badChecksum, 400},
		{"truncated zstd", []string{"zstd"}, zstdBody[:len(zstdBody)-1], 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(tc.body))
			request.Header["Content-Encoding"] = tc.encoding
			handler := handleRequestDecompression(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					writeDecompressionError(w, http.StatusBadRequest, err)
					return
				}
				t.Error("invalid body read successfully")
			}))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status || response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("status %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
		})
	}
}

func TestCompressedBodyCredentials(t *testing.T) {
	authorizer, err := static.New("test-key")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(&config.Config{Authorizers: []auth.Provider{authorizer}})
	if err != nil {
		t.Fatal(err)
	}
	for _, encoding := range []string{"gzip", "zstd"} {
		for _, key := range []string{"test-key", "wrong-key"} {
			t.Run(encoding+"/"+key, func(t *testing.T) {
				body := compressedBody(t, encoding, []byte(`{"api_key":"`+key+`"}`))
				request := httptest.NewRequest(http.MethodPost, "/v1/tavily/probe", bytes.NewReader(body))
				request.Header.Set("Content-Encoding", encoding)
				response := httptest.NewRecorder()
				s.ServeHTTP(response, request)
				// A valid body credential passes auth and reaches route lookup.
				want := http.StatusNotFound
				if key != "test-key" {
					want = http.StatusUnauthorized
				}
				if response.Code != want {
					t.Fatalf("status %d, want %d: %s", response.Code, want, response.Body.String())
				}
			})
		}
	}
}

func TestCompressedMultipartRequests(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "vision"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "image.png")
	if err != nil {
		t.Fatal(err)
	}
	image := []byte{0, 1, 2, 255}
	if _, err := part.Write(image); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/systemone", "/v1/audio/transcriptions"} {
		for _, encoding := range []string{"gzip", "zstd"} {
			t.Run(path+"/"+encoding, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(compressedBody(t, encoding, body.Bytes())))
				request.Header.Set("Content-Encoding", encoding)
				request.Header.Set("Content-Type", writer.FormDataContentType())
				handler := handleRequestDecompression(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if err := r.ParseMultipartForm(1024); err != nil {
						t.Fatal(err)
					}
					defer r.MultipartForm.RemoveAll()
					file, header, err := r.FormFile("file")
					if err != nil {
						t.Fatal(err)
					}
					defer file.Close()
					data, err := io.ReadAll(file)
					if err != nil || !bytes.Equal(data, image) || header.Filename != "image.png" || r.FormValue("model") != "vision" {
						t.Errorf("lost multipart fields or file: %v", err)
					}
					w.WriteHeader(http.StatusNoContent)
				}))
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusNoContent {
					t.Fatalf("status %d: %s", response.Code, response.Body.String())
				}
			})
		}
	}
}

func TestRequestDecompressorRejectsLargeZstdWindow(t *testing.T) {
	encoder, err := zstd.NewWriter(nil, zstd.WithWindowSize(16<<20), zstd.WithSingleSegment(false), zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	body := encoder.EncodeAll(bytes.Repeat([]byte("x"), 9<<20), nil)
	var header zstd.Header
	if err := header.Decode(body); err != nil || header.WindowSize <= 8<<20 {
		t.Fatalf("expected a frame with a window above 8 MiB: %+v, %v", header, err)
	}
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	request.Header.Set("Content-Encoding", "zstd")
	handler := handleRequestDecompression(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			writeDecompressionError(w, http.StatusBadRequest, err)
			return
		}
		t.Error("oversized window read successfully")
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
}
