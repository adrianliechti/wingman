package tavily

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/searcher"
)

var _ searcher.Provider = &Client{}

type Client struct {
	token  string
	client *http.Client
}

func New(token string, options ...Option) (*Client, error) {
	c := &Client{
		token:  token,
		client: http.DefaultClient,
	}

	for _, option := range options {
		option(c)
	}

	if c.token == "" {
		return nil, errors.New("invalid token")
	}

	return c, nil
}

const (
	CategoryGeneral = "general"
	CategoryNews    = "news"
	CategoryFinance = "finance"
)

func (c *Client) Search(ctx context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("tavily search: query is required")
	}
	if options == nil {
		options = new(searcher.SearchOptions)
	}
	if options.Limit != nil && (*options.Limit < 0 || *options.Limit > 20) {
		return nil, errors.New("tavily search: limit must be between 0 and 20")
	}
	if len(options.Include) > 300 || len(options.Exclude) > 150 {
		return nil, errors.New("tavily search: at most 300 included and 150 excluded domains are supported")
	}
	if options.Since != nil && options.Until != nil && !options.Since.Before(*options.Until) {
		return nil, errors.New("tavily search: since must be before until")
	}
	topic, err := topic(options.Category)
	if err != nil {
		return nil, err
	}

	u, _ := url.Parse("https://api.tavily.com/search")

	body := map[string]any{
		"query":                  query,
		"search_depth":           "advanced",
		"topic":                  topic,
		"include_published_date": true,
	}

	if location := strings.ToUpper(strings.TrimSpace(options.Location)); location != "" {
		if topic != "general" {
			return nil, errors.New("tavily search: location is supported only for the general category; omit location or category")
		}
		country, ok := countries[location]
		if !ok {
			return nil, fmt.Errorf("tavily search: unsupported country code %q", options.Location)
		}
		body["country"] = country
	}

	if options.Limit != nil {
		body["max_results"] = *options.Limit
	}

	if len(options.Include) > 0 {
		body["include_domains"] = options.Include
	}

	if len(options.Exclude) > 0 {
		body["exclude_domains"] = options.Exclude
	}

	if options.Since != nil {
		body["start_date"] = options.Since.UTC().Format("2006-01-02")
	}

	if options.Until != nil {
		// Tavily accepts calendar dates. Fetch the entire final day, then
		// enforce an explicit timestamp bound on the dated results below.
		until := options.Until.UTC()
		if until.Hour() != 0 || until.Minute() != 0 || until.Second() != 0 || until.Nanosecond() != 0 {
			until = until.AddDate(0, 0, 1)
		}
		body["end_date"] = until.Format("2006-01-02")
	}
	if options.Since != nil || options.Until != nil {
		body["filter_by_published_date"] = true
	}

	req, err := http.NewRequestWithContext(ctx, "POST", u.String(), jsonReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	var data searchResult

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if data.Results == nil {
		return nil, errors.New("tavily search: invalid response: missing results array")
	}

	var results []searcher.Result

	for _, r := range data.Results {
		published, dated := parseDate(r.PublishedDate)
		if options.Since != nil || options.Until != nil {
			if !dated || (options.Since != nil && published.Before(*options.Since)) || (options.Until != nil && !published.Before(*options.Until)) {
				continue
			}
		}
		result := searcher.Result{
			Source: r.URL,

			Title:   r.Title,
			Content: r.Content,
		}

		if dated {
			result.Timestamp = &published
			result.Metadata = map[string]string{"published": published.Format(time.RFC3339Nano)}
		}

		results = append(results, result)
	}

	return results, nil
}

func (c *Client) Capabilities() searcher.Capabilities {
	return searcher.Capabilities{
		DateFilters: true,
		Categories: []searcher.Category{
			{Name: CategoryGeneral, Description: "General web search, the default when category is omitted. Country bias is supported."},
			{Name: CategoryNews, Description: "News articles and current-events coverage from media outlets; results carry estimated publication or update dates. Country bias is not supported."},
			{Name: CategoryFinance, Description: "Financial news, market data and company financial coverage. Country bias is not supported."},
		},
	}
}

// topic maps a category to Tavily's fixed topics.
func topic(category string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "", CategoryGeneral:
		return CategoryGeneral, nil
	case CategoryNews:
		return "news", nil
	case CategoryFinance, "financial report":
		return "finance", nil
	default:
		return "", fmt.Errorf("tavily search: invalid category %q; use general, news or finance", category)
	}
}

func parseDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)

	if value == "" {
		return time.Time{}, false
	}

	for _, layout := range []string{time.RFC3339, time.RFC1123, time.RFC1123Z, "2006-01-02"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), true
		}
	}

	return time.Time{}, false
}
