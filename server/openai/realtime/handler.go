package realtime

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"

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
	r.HandleFunc("/realtime/translations", h.handleRealtime)
}

func (h *Handler) handleRealtime(w http.ResponseWriter, r *http.Request) {
	model := r.URL.Query().Get("model")
	translation := strings.HasSuffix(r.URL.Path, "/translations")
	transcription := !translation && r.URL.Query().Get("intent") == "transcription"

	var realtime provider.Realtime

	if !transcription {
		var err error

		if realtime, err = h.realtime(r.Context(), model); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
	}

	if translation && realtime.Defaults().OutputLanguage == "" {
		http.Error(w, "realtime model does not translate: "+model, http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade realtime connection: %v", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(32 << 20)

	var session *openAISession

	if transcription {
		session = newTranscriptionSession(conn, h.realtime)
	} else {
		session = newOpenAISession(conn, realtime, model)
	}

	if translation {
		session.config = newTranslationConfig(realtime.Defaults())
	}

	if err := session.run(r.Context()); err != nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		log.Printf("Realtime session ended: %v", err)
	}
}

func (h *Handler) realtime(ctx context.Context, model string) (provider.Realtime, error) {
	if h.Policy != nil {
		if err := h.Policy.Verify(ctx, policy.ResourceModel, model, policy.ActionAccess); err != nil {
			return nil, err
		}
	}

	return h.Realtime(model)
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}
