package realtime

import (
	"log"
	"net/http"

	"github.com/adrianliechti/wingman/config"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

type Handler struct {
	*config.Config
}

func New(cfg *config.Config) *Handler {
	return &Handler{
		Config: cfg,
	}
}

func (h *Handler) Attach(r chi.Router) {
	r.HandleFunc("/realtime", h.handleRealtime)
}

func (h *Handler) handleRealtime(w http.ResponseWriter, r *http.Request) {
	model := r.URL.Query().Get("model")
	realtime, err := h.Realtime(model)

	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade realtime connection: %v", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(32 << 20)

	session := newOpenAISession(conn, realtime, model)
	if err := session.run(r.Context()); err != nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		log.Printf("Realtime session ended: %v", err)
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}
