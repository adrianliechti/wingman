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

	rt    http.RoundTripper
	proxy *httputil.ReverseProxy

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

	s.proxy = &httputil.ReverseProxy{
		Transport: rt,

		FlushInterval: -1,

		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(u)
			r.SetXForwarded()

			// remove trailing slash if the original request did not have one
			if !strings.HasSuffix(u.Path, "/") && r.In.URL.Path == "/" {
				r.Out.URL.Path = strings.TrimRight(r.Out.URL.Path, "/")
			}

			r.Out.Host = u.Host
		},

		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("mcp proxy: upstream request failed", "url", r.URL.String(), "error", err)
			w.WriteHeader(http.StatusBadGateway)
		},
	}

	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeHTTP(w, r)
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
