// Package keenable searches the web with Keenable (https://keenable.ai).
//
// A token is optional. Without one, requests go to the keyless
// POST /v1/search/public endpoint and name the calling app in X-Keenable-Title.
// With one, requests go to POST /v1/search with X-API-Key.
package keenable

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/searcher"
)

var _ searcher.Provider = &Client{}

const (
	defaultURL   = "https://api.keenable.ai"
	defaultTitle = "wingman"
	maxResults   = 50
)

type Client struct {
	token  string
	client *http.Client

	url   string
	title string
}

func New(token string, options ...Option) (*Client, error) {
	c := &Client{
		token:  strings.TrimSpace(token),
		client: http.DefaultClient,

		url:   defaultURL,
		title: defaultTitle,
	}

	for _, option := range options {
		option(c)
	}

	c.url = strings.TrimRight(c.url, "/")

	return c, nil
}

type searchRequest struct {
	Query      string `json:"query"`
	MaxResults *int   `json:"max_results,omitempty"`

	Site           string   `json:"site,omitempty"`
	ExcludeDomains []string `json:"exclude_domains,omitempty"`

	PublishedAfter  string `json:"published_after,omitempty"`
	PublishedBefore string `json:"published_before,omitempty"`
}

type searchResponse struct {
	Results []searchResult `json:"results"`
}

type searchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Snippet     string `json:"snippet"`
	PublishedAt string `json:"published_at"`
}

// Search runs one Keenable query, or one per included domain (Keenable's site
// filter takes a single domain) and merges them in order. Location is not
// supported by Keenable and is ignored.
func (c *Client) Search(ctx context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("keenable search: query is required")
	}

	settings := searcher.SearchOptions{}
	if options != nil {
		settings = *options
	}
	if settings.Limit != nil && (*settings.Limit < 1 || *settings.Limit > maxResults) {
		return nil, fmt.Errorf("keenable search: limit must be between 1 and %d", maxResults)
	}
	if settings.Since != nil && settings.Until != nil && !settings.Since.Before(*settings.Until) {
		return nil, errors.New("keenable search: since must be before until")
	}

	base := searchRequest{
		Query:          query,
		MaxResults:     settings.Limit,
		ExcludeDomains: settings.Exclude,
	}
	if settings.Since != nil {
		base.PublishedAfter = settings.Since.UTC().Format(time.RFC3339)
	}
	if settings.Until != nil {
		base.PublishedBefore = settings.Until.UTC().Format(time.RFC3339)
	}

	sites := []string{""}
	if len(settings.Include) > 0 {
		sites = settings.Include
	}

	var results []searcher.Result
	seen := map[string]bool{}

	for _, site := range sites {
		request := base
		request.Site = strings.TrimSpace(site)

		items, err := c.search(ctx, &request)
		if err != nil {
			return nil, err
		}

		for _, r := range items {
			if r.URL == "" || seen[r.URL] {
				continue
			}
			seen[r.URL] = true

			excerpt := r.Snippet
			if excerpt == "" {
				excerpt = r.Description
			}

			result := searcher.Result{
				Source: r.URL,

				Title:   r.Title,
				Excerpt: excerpt,
			}

			if t, err := time.Parse(time.RFC3339, r.PublishedAt); err == nil {
				result.Timestamp = &t
				result.Metadata = map[string]string{"published": t.Format(time.RFC3339)}
			}

			results = append(results, result)
		}
	}

	if settings.Limit != nil && len(results) > *settings.Limit {
		results = results[:*settings.Limit]
	}

	return results, nil
}

func (c *Client) search(ctx context.Context, request *searchRequest) ([]searchResult, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	endpoint := c.url + "/v1/search/public"
	if c.token != "" {
		endpoint = c.url + "/v1/search"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	if c.token != "" {
		req.Header.Set("X-API-Key", c.token)
	} else {
		req.Header.Set("X-Keenable-Title", c.title)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("keenable search: HTTP %d: %s", resp.StatusCode, body)
	}

	var data searchResponse
	if err := json.UnmarshalRead(resp.Body, &data); err != nil {
		return nil, err
	}
	if data.Results == nil {
		return nil, errors.New("keenable search: invalid response: missing results array")
	}

	return data.Results, nil
}

func (c *Client) SearchCapabilities() searcher.Capabilities {
	return searcher.Capabilities{
		DateFilters: true,
	}
}
