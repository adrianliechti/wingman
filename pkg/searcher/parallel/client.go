package parallel

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/searcher"
	mcptool "github.com/adrianliechti/wingman/pkg/tool/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const endpoint = "https://mcp.parallel.ai/mcp"

var _ searcher.Provider = &Client{}

type Client struct {
	mcp *mcptool.Client
}

func New(client *http.Client) (*Client, error) {
	var options []mcptool.Option
	if client != nil {
		options = append(options, mcptool.WithClient(client))
	}
	upstream, err := mcptool.New(endpoint, map[string]string{"User-Agent": "wingman"}, nil, options...)
	if err != nil {
		return nil, err
	}
	return &Client{mcp: upstream}, nil
}

func (c *Client) SearchCapabilities() searcher.Capabilities { return searcher.Capabilities{} }

func (c *Client) Search(ctx context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("parallel search: query is required")
	}
	if options == nil {
		options = &searcher.SearchOptions{}
	}
	if options.Limit != nil && *options.Limit < 1 {
		return nil, errors.New("parallel search: limit must be positive")
	}
	if options.Text != nil {
		return nil, errors.New("parallel search: integrated full text is not supported; use a scraper")
	}
	if category := strings.ToLower(strings.TrimSpace(options.Category)); category != "" && category != "general" {
		return nil, errors.New("parallel search: categories are not supported; omit category")
	}
	if options.Since != nil || options.Until != nil {
		return nil, errors.New("parallel search: publication date filters are not supported; omit since and until")
	}
	if strings.TrimSpace(options.Location) != "" {
		return nil, errors.New("parallel search: location is not supported; omit location")
	}

	include, err := domains(options.Include)
	if err != nil {
		return nil, err
	}
	exclude, err := domains(options.Exclude)
	if err != nil {
		return nil, err
	}

	objective := query
	queries := []string{query}
	if len(include) > 0 {
		queries = make([]string, 0, len(include))
		for _, domain := range include {
			queries = append(queries, query+" site:"+domain)
		}
	}
	if len(exclude) > 0 {
		objective += "\nExclude results from these domains: " + strings.Join(exclude, ", ")
	}
	value, err := c.mcp.Execute(ctx, "web_search", map[string]any{
		"objective":      objective,
		"search_queries": queries,
	})
	if err != nil {
		return nil, fmt.Errorf("parallel search: %w", err)
	}

	data, err := json.Marshal(value.(*mcp.CallToolResult).StructuredContent)
	if err != nil {
		return nil, fmt.Errorf("parallel search: invalid response: %w", err)
	}
	var response struct {
		Results []struct {
			URL         string   `json:"url"`
			Title       string   `json:"title"`
			Excerpts    []string `json:"excerpts"`
			PublishDate string   `json:"publish_date"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("parallel search: invalid response: %w", err)
	}
	if response.Results == nil {
		return nil, errors.New("parallel search: invalid response: missing results array")
	}

	results := make([]searcher.Result, 0, len(response.Results))
	for _, hit := range response.Results {
		u, err := url.Parse(hit.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
			continue
		}
		host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
		if (len(include) > 0 && !matchesDomain(host, include)) || matchesDomain(host, exclude) {
			continue
		}
		result := searcher.Result{
			Source:  hit.URL,
			Title:   hit.Title,
			Excerpt: strings.Join(hit.Excerpts, "\n\n"),
		}
		if published, err := time.Parse(time.DateOnly, hit.PublishDate); err == nil {
			result.Timestamp = &published
		}
		results = append(results, result)
		if options.Limit != nil && len(results) == *options.Limit {
			break
		}
	}
	return results, nil
}

func domains(values []string) ([]string, error) {
	domains := make([]string, 0, len(values))
	for _, value := range values {
		domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
		u, err := url.Parse("https://" + domain)
		if err != nil || domain == "" || u.Hostname() != domain || strings.ContainsAny(domain, "/:#?@[]\t\r\n ") {
			return nil, fmt.Errorf("parallel search: invalid domain %q; use a hostname", value)
		}
		domains = append(domains, domain)
	}
	return domains, nil
}

func matchesDomain(host string, domains []string) bool {
	for _, domain := range domains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}
