package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/mcp"
)

var _ mcp.Provider = (*Server)(nil)

type Server struct {
	url *url.URL

	rt    http.RoundTripper
	proxy *httputil.ReverseProxy

	iconMu sync.Mutex
	icon   atomic.Pointer[iconCache]
}

func New(rawURL string, headers map[string]string, exchanger auth.TokenExchanger) (*Server, error) {
	u, err := url.Parse(rawURL)

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
	exchanger auth.TokenExchanger
	transport http.RoundTripper
}

func (rt *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.exchanger != nil {
		caller, _ := req.Context().Value(auth.TokenContextKey).(string)

		downstream, err := rt.exchanger.Token(req.Context(), caller)

		if err != nil {
			return nil, err
		}

		if downstream == "" {
			// The proxy copies the inbound Authorization header. It is the
			// caller's own credential, which the upstream is not meant to see
			// unless the configured auth says so.
			req.Header.Del("Authorization")
		} else {
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
