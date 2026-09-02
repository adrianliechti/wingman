package realtime

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gorilla/websocket"
)

// proxy preserves the previous raw WebSocket forwarding behavior as a
// compatibility fallback for models that do not have a configured provider.
type proxy struct {
	baseURL string
	apiKey  string
}

func newProxy() *proxy {
	apiKey := os.Getenv("REALTIME_API_KEY")
	baseURL := os.Getenv("REALTIME_BASE_URL")

	if baseURL == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			return nil
		}

		baseURL = os.Getenv("OPENAI_BASE_URL")
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
	}

	return &proxy{baseURL: baseURL, apiKey: apiKey}
}

func (p *proxy) isAzure() bool {
	return strings.Contains(p.baseURL, "openai.azure.com") || strings.Contains(p.baseURL, "cognitiveservices.azure.com")
}

func (p *proxy) dial(r *http.Request) (*websocket.Conn, *http.Response, error) {
	u, err := url.Parse(p.baseURL)
	if err != nil {
		return nil, nil, err
	}

	if u.Scheme == "http" {
		u.Scheme = "ws"
	} else {
		u.Scheme = "wss"
	}

	u.Path = strings.TrimRight(u.Path, "/") + "/realtime"
	query := u.Query()
	if model := r.URL.Query().Get("model"); model != "" {
		query.Set("model", model)
	}
	u.RawQuery = query.Encode()

	headers := http.Header{}
	if p.apiKey != "" {
		if p.isAzure() {
			headers.Set("api-key", p.apiKey)
		} else {
			headers.Set("Authorization", "Bearer "+p.apiKey)
		}
	}

	return (&websocket.Dialer{}).DialContext(r.Context(), u.String(), headers)
}

func (p *proxy) serve(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	downstream, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection: %v", err)
		return
	}
	defer downstream.Close()
	downstream.SetReadLimit(32 << 20)

	upstream, resp, err := p.dial(r)
	if err != nil {
		log.Printf("Failed to connect to realtime upstream: %v", err)

		if resp != nil && resp.Body != nil {
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			_ = resp.Body.Close()
			log.Print(string(data))
		}

		_ = downstream.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "upstream connection failed"))
		return
	}
	defer upstream.Close()
	upstream.SetReadLimit(32 << 20)

	go copyWebSocket(ctx, cancel, downstream, upstream, "client")
	go copyWebSocket(ctx, cancel, upstream, downstream, "upstream")

	<-ctx.Done()
}

func copyWebSocket(ctx context.Context, cancel context.CancelFunc, source, target *websocket.Conn, name string) {
	defer cancel()

	for {
		messageType, message, err := source.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Realtime %s connection error: %v", name, err)
			}
			return
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := target.WriteMessage(messageType, message); err != nil {
			log.Printf("Failed to forward realtime %s message: %v", name, err)
			return
		}
	}
}
