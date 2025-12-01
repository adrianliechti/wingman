package api

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/scraper"
)

func (h *Handler) handleExtract(w http.ResponseWriter, r *http.Request) {
	acceptType, _, _ := mime.ParseMediaType(r.Header.Get("Accept"))
	acceptJSON := acceptType == "application/json"

	model := valueModel(r)

	schema, err := valueSchema(r)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var content string
	var contentType string

	if url := valueURL(r); url != "" {
		p, err := h.Scraper(model)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		options := &scraper.ScrapeOptions{}

		result, err := p.Scrape(r.Context(), url, options)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		content = result.Text
		contentType = "text/plain"

		if acceptJSON {
			data, _ := json.Marshal(result)

			content = string(data)
			contentType = "application/json"
		}
	}

	if file, err := readFile(r); err == nil {
		p, err := h.Extractor(model)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		options := &extractor.ExtractOptions{}

		result, err := p.Extract(r.Context(), *file, options)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		content = result.Text
		contentType = "text/plain"

		if acceptJSON {
			document := Document{
				Text: result.Text,

				Pages:  []Page{},
				Blocks: []Block{},
			}

			for _, p := range result.Pages {
				document.Pages = append(document.Pages, Page{
					Page: p.Page,

					Unit:   p.Unit,
					Width:  p.Width,
					Height: p.Height,
				})
			}

			for _, b := range result.Blocks {
				document.Blocks = append(document.Blocks, Block{
					Page: b.Page,
					Text: b.Text,

					Polygon: b.Polygon,
				})
			}

			data, _ := json.Marshal(document)

			content = string(data)
			contentType = "application/json"
		}
	}

	if contentType == "" {
		writeError(w, http.StatusBadRequest, errors.New("invalid input"))
		return
	}

	if schema != nil {
		c, err := h.Completer("")

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		messages := []provider.Message{
			provider.UserMessage(content),
		}

		options := &provider.CompleteOptions{
			Schema: schema,
		}

		completion, err := c.Complete(r.Context(), messages, options)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		content = completion.Message.Text()
		contentType = "application/json"
	}

	w.Header().Set("Content-Type", contentType)
	w.Write([]byte(content))
}
