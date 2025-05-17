package api

import (
	"io"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/translator"
)

func (h *Handler) handleTranslate(w http.ResponseWriter, r *http.Request) {
	model := valueModel(r)
	language := valueLanguage(r)

	p, err := h.Translator(model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	text, err := h.readText(r)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options := &translator.TranslateOptions{
		Language: language,
	}

	translation, err := p.Translate(r.Context(), text, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	w.Header().Set("Content-Type", "text/plain")

	w.WriteHeader(http.StatusOK)
	io.WriteString(w, translation.Text)
}
