package proxy

import (
	"crypto/tls"
	"net/http"
	"net/http/httputil"
	neturl "net/url"

	"github.com/adrianliechti/wingman/pkg/mcp"
)

var _ mcp.Provider = (*Server)(nil)

type Server struct {
	url *neturl.URL

	rt http.RoundTripper
}

func New(url string) (*Server, error) {
	u, err := neturl.Parse(url)

	if err != nil {
		return nil, err
	}

	rt := &rt{}

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

			r.Out.Host = s.url.Host
		},
	}

	proxy.ServeHTTP(w, r)
}

type rt struct {
	headers map[string]string
}

func (rt *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	for key, value := range rt.headers {
		if req.Header.Get(key) != "" {
			continue // already set
		}

		req.Header.Set(key, value)
	}

	tr := http.DefaultTransport.(*http.Transport).Clone()

	tr.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true, // TODO: make configurable
	}

	return tr.RoundTrip(req)
}
