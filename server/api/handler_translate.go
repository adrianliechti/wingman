package api

import (
	"net/http"
	"strings"

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

	options := &translator.TranslateOptions{
		Language: language,
	}

	acceptText := false
	acceptHeader := strings.Split(r.Header.Get("Accept"), ", ")

	if len(acceptHeader) == 0 {
		acceptHeader = []string{"*/*"}
	}

	for _, accept := range acceptHeader {
		if strings.HasPrefix(accept, "text/") || accept == "*/*" {
			acceptText = true
			break
		}
	}

	input := translator.Input{}

	if acceptText {
		text, err := h.readText(r)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		input.Text = text
	} else {
		file, err := h.readFile(r)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		input.File = file
	}

	result, err := p.Translate(r.Context(), input, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	contentType := result.ContentType

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", contentType)
	w.Write(result.Content)
}
