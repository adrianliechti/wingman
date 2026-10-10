package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type handlerTransport struct{ handler http.Handler }

func (h handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response := httptest.NewRecorder()
	h.handler.ServeHTTP(response, request)
	return response.Result(), nil
}

// Exercise MCP's HTTP transport and authentication without binding a socket.
func newHTTPTestClient(t *testing.T, handler http.Handler, headers map[string]string, exchanger auth.TokenExchanger) (*Client, error) {
	t.Helper()
	return New("http://mcp.example/mcp", headers, exchanger, WithClient(&http.Client{Transport: handlerTransport{handler}}))
}

func TestNewPreservesHTTPClientConfiguration(t *testing.T) {
	transport := handlerTransport{http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-API-Key") != "key" {
			t.Error("missing configured header")
		}
		w.WriteHeader(http.StatusNoContent)
	})}
	hc := &http.Client{Transport: transport}
	for _, endpoint := range []string{"https://mcp.test/mcp", "https://mcp.test/sse"} {
		c, err := New(endpoint, map[string]string{"X-API-Key": "key"}, nil, WithClient(hc))
		if err != nil {
			t.Fatal(err)
		}
		var client *http.Client
		switch tr := c.transport.(type) {
		case *mcp.StreamableClientTransport:
			client = tr.HTTPClient
		case *mcp.SSEClientTransport:
			client = tr.HTTPClient
		}
		request, _ := http.NewRequest(http.MethodGet, endpoint, nil)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if _, ok := hc.Transport.(handlerTransport); !ok {
			t.Fatal("caller HTTP client was modified")
		}
	}
}

func TestNew_EndpointAndTransport(t *testing.T) {
	for _, endpoint := range []string{"", "relative/path", "ftp://example.com", "http://", "https://?host=example.com"} {
		if _, err := New(endpoint, nil, nil); err == nil {
			t.Fatalf("accepted %q", endpoint)
		}
	}
	for _, test := range []struct {
		endpoint string
		sse      bool
	}{
		{"https://example.com/sse", true}, {"https://example.com/nested/sse/", true},
		{"https://example.com/mcp?return=/sse", false}, {"https://example.com/ssearch", false},
	} {
		c, err := New(test.endpoint, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, sse := c.transport.(*mcp.SSEClientTransport)
		if sse != test.sse {
			t.Fatalf("endpoint %q: SSE = %v", test.endpoint, sse)
		}
	}
}

func TestRoundTrip_PreservesRequestAndHeaderConfiguration(t *testing.T) {
	headers := map[string]string{"X-API-Key": "configured"}
	handler := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer DOWNSTREAM-user" || request.Header.Get("X-API-Key") != "configured" {
			t.Errorf("outgoing headers %v", request.Header)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	c, err := newHTTPTestClient(t, handler, headers, stubExchanger{})
	if err != nil {
		t.Fatal(err)
	}
	headers["X-API-Key"] = "changed"
	request, _ := http.NewRequestWithContext(context.WithValue(t.Context(), auth.TokenContextKey, "user"), "POST", "http://mcp.example/mcp", nil)
	request.Header.Set("Authorization", "original")
	roundTripper := c.transport.(*mcp.StreamableClientTransport).HTTPClient.Transport
	response, err := roundTripper.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if request.Header.Get("Authorization") != "original" || request.Header.Get("X-API-Key") != "" {
		t.Fatalf("mutated incoming headers %v", request.Header)
	}
}
