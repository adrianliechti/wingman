package duckduckgo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/pkg/text"
	"golang.org/x/net/html"
)

var _ searcher.Provider = &Client{}

type Client struct {
	client *http.Client
}

func New(options ...Option) (*Client, error) {
	c := &Client{
		client: http.DefaultClient,
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

func (c *Client) Search(ctx context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("duckduckgo search: query is required")
	}
	if options == nil {
		options = new(searcher.SearchOptions)
	}
	if options.Limit != nil && *options.Limit < 1 {
		return nil, errors.New("duckduckgo search: limit must be positive")
	}
	if category := strings.ToLower(strings.TrimSpace(options.Category)); category != "" && category != "general" {
		return nil, errors.New("duckduckgo search: categories are not supported; omit category")
	}
	if options.Since != nil || options.Until != nil {
		return nil, errors.New("duckduckgo search: publication date bounds are not supported by this HTML adapter; omit since and until")
	}
	var region string
	if location := strings.ToUpper(strings.TrimSpace(options.Location)); location != "" {
		var ok bool
		region, ok = regions[location]
		if !ok {
			return nil, fmt.Errorf("duckduckgo search: unsupported country code %q", options.Location)
		}
	}
	include, err := domainNames(options.Include)
	if err != nil {
		return nil, err
	}
	exclude, err := domainNames(options.Exclude)
	if err != nil {
		return nil, err
	}
	for _, domain := range exclude {
		query += " -site:" + domain
	}
	queries := []string{query}
	if len(include) > 0 {
		queries = nil
		for _, domain := range include {
			// A separate site query for each domain gives union semantics.
			queries = append(queries, query+" site:"+domain)
		}
	}
	var results []searcher.Result
	seen := make(map[string]bool)
	for _, query := range queries {
		hits, err := c.search(ctx, query, region)
		if err != nil {
			return nil, err
		}
		for _, hit := range hits {
			u, err := url.Parse(hit.Source)
			if err != nil || (len(include) > 0 && !matchesDomain(u.Hostname(), include)) || matchesDomain(u.Hostname(), exclude) || seen[hit.Source] {
				continue
			}
			seen[hit.Source] = true
			results = append(results, hit)
			if options.Limit != nil && len(results) >= *options.Limit {
				return results, nil
			}
		}
	}
	return results, nil
}

func (c *Client) search(ctx context.Context, query, region string) ([]searcher.Result, error) {
	u, _ := url.Parse("https://html.duckduckgo.com/html/")
	values := u.Query()
	values.Set("q", query)
	if region != "" {
		values.Set("kl", region)
	}
	u.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://www.duckduckgo.com/")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.4 Safari/605.1.15")

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("duckduckgo search: HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return parseResults(resp.Body)
}

func (c *Client) Capabilities() searcher.Capabilities { return searcher.Capabilities{} }

func parseResults(body io.Reader) ([]searcher.Result, error) {
	doc, err := html.Parse(body)
	if err != nil {
		return nil, err
	}
	var results []searcher.Result
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if hasClass(n, "result") {
			if hasClass(n, "result--ad") {
				return
			}
			var result searcher.Result
			var read func(*html.Node)
			read = func(child *html.Node) {
				if child.Type == html.ElementNode && child.Data == "a" && hasClass(child, "result__a") {
					result.Title = nodeText(child)
					for _, attr := range child.Attr {
						if attr.Key == "href" {
							result.Source = sourceURL(attr.Val)
						}
					}
				}
				if hasClass(child, "result__snippet") {
					result.Content = nodeText(child)
				}
				for next := child.FirstChild; next != nil; next = next.NextSibling {
					read(next)
				}
			}
			read(n)
			if result.Source != "" {
				results = append(results, result)
			}
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return results, nil
}

func hasClass(n *html.Node, name string) bool {
	for _, attr := range n.Attr {
		if attr.Key == "class" {
			for _, class := range strings.Fields(attr.Val) {
				if class == name {
					return true
				}
			}
		}
	}
	return false
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return text.Normalize(b.String())
}

func sourceURL(value string) string {
	u, err := url.Parse(value)
	if err != nil {
		return ""
	}
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	if u.Host == "" {
		u.Host = "html.duckduckgo.com"
	}
	if (u.Hostname() == "duckduckgo.com" || u.Hostname() == "html.duckduckgo.com") && u.Path == "/l/" {
		u, err = url.Parse(u.Query().Get("uddg"))
		if err != nil {
			return ""
		}
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return u.String()
}

func domainNames(values []string) ([]string, error) {
	var domains []string
	for _, value := range values {
		domain := strings.ToLower(strings.TrimSpace(value))
		u, err := url.Parse("https://" + domain)
		if err != nil || domain == "" || u.Hostname() != domain || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(domain, " \t\r\n") {
			return nil, fmt.Errorf("duckduckgo search: invalid domain %q; use a hostname", value)
		}
		domains = append(domains, domain)
	}
	return domains, nil
}

func matchesDomain(host string, domains []string) bool {
	host = strings.ToLower(host)
	for _, domain := range domains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}
