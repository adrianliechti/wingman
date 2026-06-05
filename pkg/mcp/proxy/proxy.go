package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	neturl "net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/auth/obo"
	"github.com/adrianliechti/wingman/pkg/mcp"
)

var _ mcp.Provider = (*Server)(nil)

type Server struct {
	url *neturl.URL

	rt http.RoundTripper

	iconMu sync.Mutex
	icon   atomic.Pointer[iconCache]
}

func New(url string, headers map[string]string, exchanger *obo.Exchanger) (*Server, error) {
	u, err := neturl.Parse(url)

	if err != nil {
		return nil, err
	}

	rt := &rt{
		headers:   headers,
		exchanger: exchanger,
		transport: http.DefaultTransport,
	}

	s := &Server{
		url: u,

		rt: rt,
	}

	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	proxy := &httputil.ReverseProxy{
		Transport: s.rt,

		FlushInterval: -1,

		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(s.url)
			r.SetXForwarded()

			// remove trailing slash if the original request did not have one
			if !strings.HasSuffix(s.url.Path, "/") && r.In.URL.Path == "/" {
				r.Out.URL.Path = strings.TrimRight(r.Out.URL.Path, "/")
			}

			r.Out.Host = s.url.Host
		},

		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("mcp proxy: upstream request failed", "url", r.URL.String(), "error", err)
			w.WriteHeader(http.StatusBadGateway)
		},
	}

	proxy.ServeHTTP(w, r)
}

type rt struct {
	headers   map[string]string
	exchanger *obo.Exchanger
	transport http.RoundTripper
}

func (rt *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.exchanger != nil {
		if token, _ := req.Context().Value(auth.TokenContextKey).(string); token != "" {
			downstream, err := rt.exchanger.Token(req.Context(), token)

			if err != nil {
				return nil, err
			}

			req.Header.Set("Authorization", "Bearer "+downstream)
		}
	}

	for key, value := range rt.headers {
		if req.Header.Get(key) != "" {
			continue // already set
		}

		req.Header.Set(key, value)
	}

	return rt.transport.RoundTrip(req)
}
