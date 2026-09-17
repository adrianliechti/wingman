package harness

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync"
	"testing"
)

// Exchange contains the wire protocol without authentication or cookie headers.
type Exchange struct {
	Method          string      `json:"method"`
	Path            string      `json:"path"`
	RequestHeaders  http.Header `json:"request_headers"`
	Request         string      `json:"request"`
	Status          int         `json:"status"`
	ResponseHeaders http.Header `json:"response_headers"`
	Response        string      `json:"response"`
}

type Recorder struct {
	*httptest.Server
	mu        sync.Mutex
	exchanges []Exchange
}

// NewRecorder forwards requests to baseURL with the supplied authentication.
// Responses are flushed as they arrive, including before a stream completes.
// A positive maxRequests limits upstream calls for unattended CLI test runs.
func NewRecorder(t *testing.T, baseURL string, auth http.Header, maxRequests int) *Recorder {
	t.Helper()
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		t.Fatal("endpoint must be an HTTP(S) base URL without credentials, query or fragment")
	}
	r := &Recorder{}
	proxy := &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(u)
			p.Out.Header.Del("Accept-Encoding")
			p.Out.Header.Del("Authorization")
			p.Out.Header.Del("X-Api-Key")
			p.Out.Header.Del("Cookie")
			for key, values := range auth {
				p.Out.Header[key] = append([]string(nil), values...)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			http.Error(w, "upstream request failed: "+err.Error(), http.StatusBadGateway)
		},
	}
	requests := 0
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			http.Error(w, "read request: "+err.Error(), http.StatusBadRequest)
			return
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		record := Exchange{Method: req.Method, Path: req.URL.RequestURI(), RequestHeaders: protocolHeaders(req.Header), Request: string(body)}
		cw := &captureWriter{ResponseWriter: w}
		defer func() {
			// Retain partial responses if ReverseProxy aborts a broken stream.
			record.Status, record.Response = cw.status, cw.body.String()
			record.ResponseHeaders = protocolHeaders(w.Header())
			r.mu.Lock()
			r.exchanges = append(r.exchanges, record)
			r.mu.Unlock()
		}()
		r.mu.Lock()
		requests++
		overLimit := maxRequests > 0 && requests > maxRequests
		r.mu.Unlock()
		if overLimit {
			http.Error(cw, "CLI test request limit exceeded", http.StatusTooManyRequests)
			return
		}
		proxy.ServeHTTP(cw, req)
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *Recorder) Exchanges() []Exchange {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Exchange(nil), r.exchanges...)
}

func protocolHeaders(headers http.Header) http.Header {
	result := http.Header{}
	for _, key := range []string{"Content-Type", "Content-Encoding", "Anthropic-Version", "Anthropic-Beta", "Openai-Beta", "User-Agent", "Request-Id", "X-Request-Id"} {
		if values := headers.Values(key); len(values) > 0 {
			result[key] = append([]string(nil), values...)
		}
	}
	return result
}

type captureWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *captureWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *captureWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.body.Write(p[:n])
	return n, err
}

func (w *captureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
