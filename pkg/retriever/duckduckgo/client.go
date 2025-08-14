package duckduckgo

import (
	"bufio"
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/adrianliechti/wingman/pkg/retriever"
	"github.com/adrianliechti/wingman/pkg/text"
)

var _ retriever.Provider = &Client{}

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

func (c *Client) Retrieve(ctx context.Context, query string, options *retriever.RetrieveOptions) ([]retriever.Result, error) {
	url, _ := url.Parse("https://duckduckgo.com/html/")

	values := url.Query()
	values.Set("q", query)

	url.RawQuery = values.Encode()

	req, _ := http.NewRequestWithContext(ctx, "GET", url.String(), nil)
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

	var results []retriever.Result

	regexLink := regexp.MustCompile(`href="([^"]+)"`)
	regexSnippet := regexp.MustCompile(`<[^>]*>`)

	scanner := bufio.NewScanner(resp.Body)

	var resultURL string
	var resultTitle string
	var resultSnippet string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.Contains(line, "result__a") {
			snippet := regexSnippet.ReplaceAllString(line, "")
			snippet = text.Normalize(snippet)

			resultTitle = snippet
		}

		if strings.Contains(line, "result__url") {
			links := regexLink.FindStringSubmatch(line)

			if len(links) >= 2 {
				resultURL = links[1]
			}
		}

		if strings.Contains(line, "result__snippet") {
			snippet := regexSnippet.ReplaceAllString(line, "")
			snippet = text.Normalize(snippet)

			resultSnippet = snippet
		}

		if resultSnippet == "" {
			continue
		}

		result := retriever.Result{
			Source: resultURL,

			Title:   resultTitle,
			Content: resultSnippet,
		}

		results = append(results, result)

		resultURL = ""
		resultTitle = ""
		resultSnippet = ""
	}

	return results, nil
}
