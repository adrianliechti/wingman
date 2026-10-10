package api

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/policy"
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

	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, model, policy.ActionAccess); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	options := &translator.TranslateOptions{
		Language: language,
	}

	acceptText := false
	acceptHeader := strings.Split(r.Header.Get("Accept"), ",")

	if len(acceptHeader) == 0 || r.Header.Get("Accept") == "" {
		acceptHeader = []string{"*/*"}
	}

	for _, accept := range acceptHeader {
		accept, _, _ := mime.ParseMediaType(strings.TrimSpace(accept))

		if strings.HasPrefix(accept, "text/") || accept == "*/*" {
			acceptText = true
			break
		}
	}

	input := translator.Input{}
	capabilities := p.Capabilities()

	if acceptText {
		input, err = h.readTranslationText(r, capabilities)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	} else {
		if !capabilities.FileToDocument.MaySupport() && !capabilities.FileToText.MaySupport() {
			writeError(w, http.StatusBadRequest, fmt.Errorf("translator does not support file input: %w", translator.ErrUnsupported))
			return
		}
		if !capabilities.FileToDocument.MaySupport() {
			writeError(w, http.StatusNotAcceptable, errors.New("translator returns plain text, not translated documents; use Accept: text/plain"))
			return
		}
		file, err := readFile(r)

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
	if result == nil {
		writeError(w, http.StatusBadGateway, errors.New("translator returned an empty response"))
		return
	}

	contentType := result.ContentType

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", contentType)
	w.Write(result.Content)
}

// Prefer confirmed file-to-text support. When a custom service's modes are
// unknown, retain extraction plus text translation if text input is a candidate.
func (h *Handler) readTranslationText(r *http.Request, capabilities translator.Capabilities) (translator.Input, error) {
	if valueInput(r) != "" || valueURL(r) != "" {
		if !capabilities.TextToText.MaySupport() {
			return translator.Input{}, fmt.Errorf("translator does not support text input: %w", translator.ErrUnsupported)
		}
		text, err := h.readText(r)
		return translator.Input{Text: text}, err
	}
	if !capabilities.FileToText.MaySupport() && !capabilities.TextToText.MaySupport() {
		return translator.Input{}, fmt.Errorf("translator does not support text output for this input: %w", translator.ErrUnsupported)
	}
	file, err := readFile(r)
	if err != nil {
		return translator.Input{}, err
	}
	if capabilities.FileToText == translator.Supported || !capabilities.TextToText.MaySupport() {
		return translator.Input{File: file}, nil
	}
	text, err := h.extractText(r, file)
	return translator.Input{Text: text}, err
}
