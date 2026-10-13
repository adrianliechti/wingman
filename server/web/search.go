package web

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/guard"
	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/searcher"
)

type Retriever struct{ *config.Config }

func New(cfg *config.Config) *Retriever { return &Retriever{Config: cfg} }

type requestError struct {
	status  int
	message string
}

func (e *requestError) Error() string { return e.message }

func BadRequest(message string) error { return &requestError{400, message} }

func StatusOf(err error) int {
	var request *requestError
	if errors.As(err, &request) {
		return request.status
	}

	var flagged *guard.FlaggedError
	if errors.As(err, &flagged) {
		return http.StatusBadRequest
	}
	return http.StatusBadGateway
}

func (h *Retriever) Search(r *http.Request, model, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	settings := searcher.SearchOptions{}
	if options != nil {
		settings = *options
	}
	options = &settings
	if strings.TrimSpace(query) == "" {
		return nil, BadRequest("query is required")
	}
	if options.Since != nil && options.Until != nil && !options.Since.Before(*options.Until) {
		return nil, BadRequest("start date must be before end date")
	}
	p, err := h.Searcher(model)
	if err != nil {
		return nil, BadRequest(err.Error())
	}
	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, model, policy.ActionAccess); err != nil {
		return nil, &requestError{404, "searcher not found"}
	}
	caps := p.Capabilities()
	if (options.Since != nil || options.Until != nil) && !caps.DateFilters {
		return nil, BadRequest("selected searcher does not support publication date filters")
	}
	category := options.Category
	if category == "general" {
		category = ""
	}
	// Map Tavily's finance topic and Exa's financial report category onto each
	// other only when the backend lacks the requested name.
	if alias := map[string]string{"finance": "financial report", "financial report": "finance"}[category]; alias != "" && !hasCategory(caps, category) && hasCategory(caps, alias) {
		category = alias
	}
	if category != "" && !hasCategory(caps, category) {
		return nil, BadRequest(fmt.Sprintf("selected searcher does not support category %q", options.Category))
	}
	options.Category = category
	for _, domain := range append(slices.Clone(options.Include), options.Exclude...) {
		if strings.TrimSpace(domain) == "" || strings.ContainsAny(domain, "/: \t\r\n") {
			return nil, BadRequest("domain filters must contain hostnames")
		}
	}
	if options.Limit != nil && *options.Limit == 0 {
		return []searcher.Result{}, nil
	}
	results, err := h.SearchProvider(r, p, model, query, options)
	if err != nil {
		return nil, err
	}
	if options.Limit != nil && len(results) > *options.Limit {
		results = results[:*options.Limit]
	}
	return results, nil
}

// SearchProvider requests native page text where supported and composes a
// scraper only for providers without integrated retrieval. Excerpts remain
// available in either case, including when individual pages cannot be fetched.
func (h *Retriever) SearchProvider(r *http.Request, p searcher.Provider, model, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	settings := searcher.SearchOptions{}
	if options != nil {
		settings = *options
	}
	includeText := settings.Text != nil
	nativeText := p.Capabilities().Content
	if !nativeText {
		settings.Text = nil
	}
	results, err := p.Search(r.Context(), query, &settings)
	if err != nil || !includeText {
		return results, err
	}
	if settings.Limit != nil && *settings.Limit >= 0 && len(results) > *settings.Limit {
		results = results[:*settings.Limit]
	}
	results = slices.Clone(results)
	if nativeText {
		for i := range results {
			if results[i].Text == nil {
				empty := ""
				results[i].Text = &empty
			}
		}
		return results, nil
	}
	contents, err := h.SearchContents(r, model, results)
	if err != nil {
		return nil, err
	}
	for i := range results {
		text := ""
		if contents[i].Err == nil {
			text = contents[i].Text
		}
		results[i].Text = &text
	}
	return results, nil
}

func hasCategory(caps searcher.Capabilities, category string) bool {
	for _, c := range caps.Categories {
		if c.Name == category {
			return true
		}
	}
	return false
}
