package exa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

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

	return c, nil
}

func (c *Client) Search(ctx context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	if options == nil {
		options = new(searcher.SearchOptions)
	}

	if options.Category == "" {
		options.Category = c.category
	}

	if options.Location == "" {
		options.Location = c.location
	}

	request := &SearchRequest{
		Query: query,

		Location: options.Location,

		NumResults: options.Limit,

		IncludeDomains: options.Include,
		ExcludeDomains: options.Exclude,

		Contents: &SearchContents{
			Text: true,
		},
	}

	request.Category = options.Category

	if c.mode != "" {
		request.Type = c.mode
	}

	if len(request.ExcludeDomains) > 0 && len(request.IncludeDomains) > 0 {
		request.ExcludeDomains = nil
	}

	body, _ := json.Marshal(request)

	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.exa.ai/search", bytes.NewReader(body))
	req.Header.Set("x-api-key", c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, errors.New(string(body))
	}

	var data SearchResponse

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	var results []searcher.Result

	for _, r := range data.Results {
		result := searcher.Result{
			Source: r.URL,

			Title:   r.Title,
			Content: r.Text,
		}

		results = append(results, result)
	}

	return results, nil
}

const (
	CategoryCompany         = "company"
	CategoryPeople          = "people"
	CategoryResearchPaper   = "research paper"
	CategoryPersonalSite    = "personal site"
	CategoryFinancialReport = "financial report"
)

func (c *Client) Categories() []searcher.Category {
	return []searcher.Category{
		{Name: CategoryCompany, Description: "Specific companies or organizations (e.g. SaaS vendors, public companies). Note: domain exclusions and date filters are not supported in this category."},
		{Name: CategoryPeople, Description: "Specific people or profile pages (e.g. LinkedIn-style biographies). Note: domain exclusions and date filters are not supported in this category."},
		{Name: CategoryResearchPaper, Description: "Academic papers and peer-reviewed research publications."},
		{Name: CategoryPersonalSite, Description: "Personal websites, blogs, and homepages."},
		{Name: CategoryFinancialReport, Description: "Earnings releases, 10-K/10-Q filings, and other financial reports."},
	}
}
