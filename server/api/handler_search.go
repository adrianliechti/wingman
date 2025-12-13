package api

import (
	"net/http"

	"github.com/adrianliechti/wingman/pkg/searcher"
)

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	model := valueModel(r)

	p, err := h.Searcher(model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	query := valueInput(r)

	if query == "" {
		writeError(w, http.StatusBadRequest, nil)
		return
	}

	options := &searcher.SearchOptions{}

	if values, ok := r.Form["domain"]; ok && len(values) > 0 {
		options.Domains = append(options.Domains, values...)
	}

	results, err := p.Search(r.Context(), query, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result := make([]SearchResult, 0)

	for _, r := range results {
		segment := SearchResult{
			Source: r.Source,

			Title:   r.Title,
			Content: r.Content,

			Metadata: r.Metadata,
		}

		result = append(result, segment)
	}

	writeJson(w, result)
}

type SearchResult struct {
	Source string `json:"source,omitempty"`

	Title   string `json:"title,omitempty"`
	Content string `json:"content,omitempty"`

	Metadata map[string]string `json:"metadata,omitempty"`
}
