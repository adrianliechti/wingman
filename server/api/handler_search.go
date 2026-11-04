package api

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/server/web"
)

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	request, err := readSearchRequest(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	model := request.Model

	p, err := h.Searcher(model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, model, policy.ActionAccess); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	var query string
	for _, value := range []string{request.Input, request.Prompt, request.Query, request.Instructions} {
		if strings.TrimSpace(value) != "" {
			query = value
			break
		}
	}

	if strings.TrimSpace(query) == "" {
		writeError(w, http.StatusBadRequest, errors.New("query or input is required"))
		return
	}

	since, err := searcher.ParseDateBound(request.Since, false)

	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid since: %w", err))
		return
	}

	until, err := searcher.ParseDateBound(request.Until, true)

	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid until: %w", err))
		return
	}

	options := &searcher.SearchOptions{
		Limit: request.Limit,

		Category: request.Category,
		Location: request.Location,

		Since: since,
		Until: until,
	}
	options.Text, err = searchTextOptions(request.Text)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	for _, v := range request.Domain {
		if val, found := strings.CutPrefix(v, "!"); found {
			options.Exclude = append(options.Exclude, val)
		} else {
			options.Include = append(options.Include, v)
		}
	}

	results, err := web.New(h.Config).SearchProvider(r, p, model, query, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result := make([]SearchResult, 0)

	for _, r := range results {
		segment := SearchResult{
			Source: r.Source,

			Title:   r.Title,
			Excerpt: r.Excerpt,

			Metadata: r.Metadata,
		}
		if options.Text != nil && r.Text != nil && *r.Text != "" {
			segment.Text = r.Text
		}

		if r.Timestamp != nil {
			if segment.Metadata == nil {
				segment.Metadata = map[string]string{}
			}

			if _, ok := segment.Metadata["published"]; !ok {
				segment.Metadata["published"] = r.Timestamp.UTC().Format("2006-01-02T15:04:05Z07:00")
			}
		}

		result = append(result, segment)
	}

	writeJson(w, result)
}

type SearchResult struct {
	Source string `json:"source,omitzero"`

	Title   string  `json:"title,omitzero"`
	Excerpt string  `json:"excerpt,omitzero"`
	Text    *string `json:"text,omitzero"`

	Metadata map[string]string `json:"metadata,omitempty"`
}

type SearchRequest struct {
	Model        string         `json:"model"`
	Query        string         `json:"query"`
	Input        string         `json:"input"`
	Prompt       string         `json:"prompt"`
	Instructions string         `json:"instructions"`
	Limit        *int           `json:"limit"`
	Category     string         `json:"category"`
	Location     string         `json:"location"`
	Domain       []string       `json:"domain"`
	Since        string         `json:"since"`
	Until        string         `json:"until"`
	Text         jsontext.Value `json:"text"`
}

func readSearchRequest(w http.ResponseWriter, r *http.Request) (*SearchRequest, error) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil && r.Header.Get("Content-Type") != "" {
		return nil, err
	}
	if media == "application/json" {
		var request SearchRequest
		if err := web.ReadRequest(w, r, &request); err != nil {
			return nil, err
		}
		return &request, nil
	}
	if media == "multipart/form-data" {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return nil, err
		}
	} else if err := r.ParseForm(); err != nil {
		return nil, err
	}
	// Text is a retrieval selector, never an alias for the search query.
	return &SearchRequest{
		Model: valueModel(r), Query: r.FormValue("query"), Input: r.FormValue("input"),
		Prompt: r.FormValue("prompt"), Instructions: r.FormValue("instructions"),
		Limit: valueLimit(r), Category: valueCategory(r), Location: valueLocation(r),
		Domain: r.Form["domain"], Since: r.FormValue("since"), Until: r.FormValue("until"),
		Text: jsontext.Value(r.FormValue("text")),
	}, nil
}

func searchTextOptions(raw jsontext.Value) (*searcher.TextOptions, error) {
	value := bytes.TrimSpace(raw)
	switch string(value) {
	case "", "null", "false":
		return nil, nil
	case "true":
		return &searcher.TextOptions{}, nil
	}
	if len(value) == 0 || value[0] != '{' {
		return nil, errors.New("text must be an options object, true, false or null")
	}
	var options searcher.TextOptions
	if err := json.Unmarshal(value, &options, json.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	return &options, nil
}
