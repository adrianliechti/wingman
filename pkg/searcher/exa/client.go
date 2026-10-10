package exa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/searcher"
)

var _ searcher.Provider = &Client{}

type Client struct {
	token  string
	client *http.Client

	mode string

	category string
	location string
}

func New(token string, options ...Option) (*Client, error) {
	c := &Client{
		token:  token,
		client: http.DefaultClient,

		mode: "fast",
	}

	for _, option := range options {
		option(c)
	}

	if c.token == "" {
		return nil, errors.New("invalid token")
	}

	// Search stays retrieval-only. Synthesis belongs to Wingman's own model.
	switch c.mode {
	case "fast", "instant", "auto":
	default:
		return nil, fmt.Errorf("exa search: mode %q is not a retrieval-only search mode", c.mode)
	}

	return c, nil
}

func (c *Client) Search(ctx context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("exa search: query is required")
	}

	// Do not mutate caller-owned options (they may be reused concurrently).
	settings := searcher.SearchOptions{}
	if options != nil {
		settings = *options
	}
	if settings.Limit != nil && (*settings.Limit < 1 || *settings.Limit > 100) {
		return nil, errors.New("exa search: limit must be between 1 and 100")
	}
	if settings.Category == "" {
		settings.Category = c.category
	}
	settings.Category = strings.ToLower(strings.TrimSpace(settings.Category))
	if settings.Category == CategoryResearchPaper {
		settings.Category = CategoryPublication
	}
	if settings.Location == "" {
		settings.Location = c.location
	}
	settings.Location = strings.ToUpper(strings.TrimSpace(settings.Location))
	if settings.Location != "" && !countryCode(settings.Location) {
		return nil, errors.New("exa search: location must be a two-letter ISO 3166-1 country code")
	}
	if len(settings.Include) > 1200 || len(settings.Exclude) > 1200 {
		return nil, errors.New("exa search: at most 1200 included or excluded domains are supported")
	}
	if settings.Category == CategoryCompany || settings.Category == CategoryPeople {
		if settings.Since != nil || settings.Until != nil || len(settings.Exclude) > 0 {
			return nil, fmt.Errorf("exa search: category %q does not support date filters or excluded domains; omit those filters or use another category", settings.Category)
		}
	}

	request := &SearchRequest{
		Query: query,

		Location: settings.Location,

		NumResults: settings.Limit,

		IncludeDomains: settings.Include,
		ExcludeDomains: settings.Exclude,

		Contents: &SearchContents{
			// Plain source text only: no Exa highlights, summaries or outputSchema.
			// Select a query-relevant excerpt locally after retrieval.
			Text: true,
		},
	}

	request.Category = settings.Category

	if settings.Since != nil && settings.Until != nil && !settings.Since.Before(*settings.Until) {
		return nil, errors.New("exa search: since must be before until")
	}

	if settings.Since != nil {
		request.StartPublishedDate = settings.Since.UTC().Format(time.RFC3339Nano)
	}

	if settings.Until != nil {
		request.EndPublishedDate = settings.Until.UTC().Format(time.RFC3339Nano)
	}

	if c.mode != "" {
		request.Type = c.mode
	}

	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.exa.ai/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("exa search: HTTP %d: %s", resp.StatusCode, body)
	}

	var data SearchResponse

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if data.Results == nil {
		return nil, errors.New("exa search: invalid response: missing results array")
	}

	var results []searcher.Result

	for _, r := range data.Results {
		result := searcher.Result{
			Source: r.URL,

			Title:   r.Title,
			Content: searchExcerpt(r.Text, query),
		}

		if t, err := time.Parse(time.RFC3339, r.PublishedDate); err == nil {
			result.Timestamp = &t
			result.Metadata = map[string]string{"published": t.Format(time.RFC3339)}
		}

		results = append(results, result)
	}

	return results, nil
}

const (
	CategoryCompany     = "company"
	CategoryPeople      = "people"
	CategoryNews        = "news"
	CategoryPublication = "publication"
	// CategoryResearchPaper is a legacy alias for CategoryPublication.
	CategoryResearchPaper   = "research paper"
	CategoryPersonalSite    = "personal site"
	CategoryFinancialReport = "financial report"
)

func (c *Client) Capabilities() searcher.Capabilities {
	return searcher.Capabilities{
		DateFilters: true,
		Categories: []searcher.Category{
			{Name: CategoryCompany, Description: "Specific companies or organizations (e.g. SaaS vendors, public companies). Date filters and domain exclusions are not supported; omit those filters or choose another category."},
			{Name: CategoryPeople, Description: "Specific people or profile pages (e.g. LinkedIn-style biographies). Date filters and domain exclusions are not supported; omit those filters or choose another category."},
			{Name: CategoryNews, Description: "News articles and current-events coverage from media outlets."},
			{Name: CategoryPublication, Description: "Scholarly publications, including research papers, preprints, and journal articles."},
			{Name: CategoryPersonalSite, Description: "Personal websites, blogs, and homepages."},
			{Name: CategoryFinancialReport, Description: "Earnings releases, 10-K/10-Q filings, and other financial reports."},
		},
	}
}

func countryCode(value string) bool {
	return len(value) == 2 && value[0] >= 'A' && value[0] <= 'Z' && value[1] >= 'A' && value[1] <= 'Z'
}
