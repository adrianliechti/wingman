package exa

import (
	"net/http"
	"time"

	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/server/web"

	"github.com/google/uuid"
)

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	var request SearchRequest
	if err := web.ReadCompatibleRequest(w, r, &request); err != nil {
		web.WriteError(w, r, 400, err.Error())
		return
	}
	model, err := request.Provider(r)
	if err != nil {
		web.WriteError(w, r, 400, err.Error())
		return
	}
	limit := 10
	if request.NumResults != nil {
		limit = *request.NumResults
	}
	if limit < 1 || limit > 20 {
		web.WriteError(w, r, 400, "numResults must be between 1 and 20")
		return
	}
	options := searcher.SearchOptions{Limit: &limit, Category: request.Category, Location: request.Location, Include: request.Include, Exclude: request.Exclude}
	if len(options.Include) > 300 || len(options.Exclude) > 150 {
		web.WriteError(w, r, 400, "at most 300 included and 150 excluded domains are supported")
		return
	}
	options.Since, err = exaDate(request.Start)
	if err != nil {
		web.WriteError(w, r, 400, "invalid startPublishedDate")
		return
	}
	options.Until, err = exaDate(request.End)
	if err != nil {
		web.WriteError(w, r, 400, "invalid endPublishedDate")
		return
	}
	text, maxCharacters := false, 0
	highlights, maxHighlightCharacters := false, 0
	if request.Contents != nil {
		text, maxCharacters, err = contentSelector(request.Contents.Text)
		if err != nil {
			web.WriteError(w, r, 400, err.Error())
			return
		}
		highlights, maxHighlightCharacters, err = contentSelector(request.Contents.Highlights)
		if err != nil {
			web.WriteError(w, r, 400, "invalid highlights: "+err.Error())
			return
		}
	}
	if text {
		options.Text = &searcher.TextOptions{}
	}
	results, err := h.Search(r, model, request.Query, &options)
	if err != nil {
		web.WriteError(w, r, web.StatusOf(err), err.Error())
		return
	}
	response := Response{RequestID: uuid.NewString(), Results: []Result{}}
	var contents []web.Extraction
	if text {
		contents, err = h.SearchContents(r, model, results)
		if err != nil {
			web.WriteError(w, r, web.StatusOf(err), err.Error())
			return
		}
	}
	for i, result := range results {
		hit := Result{ID: result.Source, URL: result.Source, Title: result.Title}
		if result.Timestamp != nil {
			hit.PublishedDate = result.Timestamp.UTC().Format(time.RFC3339)
		}
		if highlights {
			hit.Highlights = []string{}
			if result.Excerpt != "" {
				hit.Highlights = []string{web.Truncate(result.Excerpt, maxHighlightCharacters)}
			}
		}
		if text {
			response.Statuses = append(response.Statuses, status(contents[i]))
			if contents[i].Err == nil {
				content := web.Truncate(contents[i].Text, maxCharacters)
				hit.Text = &content
			}
		}
		response.Results = append(response.Results, hit)
	}
	response.SearchTime = float64(time.Since(started).Microseconds()) / 1000
	web.WriteJSON(w, response)
}

func exaDate(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t, err = time.Parse("2006-01-02", value)
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}
