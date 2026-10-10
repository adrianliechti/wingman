package proxy

import (
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func headerEcho(t *testing.T) (http.RoundTripper, chan string) {
	t.Helper()

	seen := make(chan string, 4)

	s := handlerTransport{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Authorization")

		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{1, 2, 3})
	})}

	return s, seen
}

// TestIconFetchWithholdsCredentialsCrossOrigin guards against leaking the
// configured headers: icon sources are chosen by the upstream MCP server and
// may point anywhere.
func TestIconFetchWithholdsCredentialsCrossOrigin(t *testing.T) {
	foreign, seen := headerEcho(t)
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: foreign}
	t.Cleanup(func() { http.DefaultClient = previous })

	s, err := New("http://127.0.0.1:1/mcp", map[string]string{"Authorization": "Bearer SECRET"}, nil)

	if err != nil {
		t.Fatal(err)
	}

	icon := mcp.Icon{Source: "https://foreign.test/icon.png", MIMEType: "image/png"}

	if _, _, ok := resolveIcon(s.iconClient(icon.Source), icon); !ok {
		t.Fatal("icon did not resolve")
	}

	if got := <-seen; got != "" {
		t.Errorf("cross-origin icon host received credential %q", got)
	}
}

// TestIconFetchSendsCredentialsSameOrigin asserts the configured headers still
// reach the MCP server's own origin, which may require them to serve the icon.
func TestIconFetchSendsCredentialsSameOrigin(t *testing.T) {
	upstream, seen := headerEcho(t)

	s, err := New("https://mcp.test/mcp", map[string]string{"Authorization": "Bearer SECRET"}, nil, WithTransport(upstream))

	if err != nil {
		t.Fatal(err)
	}

	icon := mcp.Icon{Source: "https://mcp.test/icon.png", MIMEType: "image/png"}

	if _, _, ok := resolveIcon(s.iconClient(icon.Source), icon); !ok {
		t.Fatal("icon did not resolve")
	}

	if got := <-seen; got != "Bearer SECRET" {
		t.Errorf("same-origin icon host got %q, want the configured credential", got)
	}
}

func TestResolveIconDataURI(t *testing.T) {
	icon := mcp.Icon{Source: "data:image/png;base64,AQID"}

	contentType, data, ok := resolveIcon(nil, icon)

	if !ok || contentType != "image/png" || len(data) != 3 {
		t.Errorf("got %q %v %v", contentType, data, ok)
	}
}
