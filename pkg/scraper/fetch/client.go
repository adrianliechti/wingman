package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/scraper"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var _ scraper.Provider = &Client{}

type Client struct {
	client *http.Client
}

func New(options ...Option) (*Client, error) {
	c := &Client{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

func (c *Client) Scrape(ctx context.Context, url string, options *scraper.ScrapeOptions) (*scraper.Document, error) {
	if options == nil {
		options = new(scraper.ScrapeOptions)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)

	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Wingman/1.0)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status: %s", resp.Status)
	}

	contentType := resp.Header.Get("Content-Type")

	if strings.Contains(contentType, "text/plain") {
		data, err := io.ReadAll(resp.Body)

		if err != nil {
			return nil, err
		}

		return &scraper.Document{
			Text: string(data),
		}, nil
	}

	doc, err := html.Parse(resp.Body)

	if err != nil {
		return nil, err
	}

	// Remove non-content elements before extracting text.
	removeElements(doc, atom.Script)
	removeElements(doc, atom.Style)
	removeElements(doc, atom.Noscript)
	removeElements(doc, atom.Iframe)
	removeElements(doc, atom.Svg)
	removeElements(doc, atom.Nav)
	removeElements(doc, atom.Footer)
	removeElements(doc, atom.Header)

	// Also remove elements by common non-content roles/classes.
	removeByAttr(doc, "role", "navigation")
	removeByAttr(doc, "role", "banner")
	removeByAttr(doc, "role", "contentinfo")
	removeByAttr(doc, "aria-hidden", "true")

	// Try to find the main content area first; fall back to the full body.
	root := findElement(doc, atom.Main)

	if root == nil {
		root = findElement(doc, atom.Article)
	}

	if root == nil {
		root = findByAttr(doc, "role", "main")
	}

	if root == nil {
		root = findElement(doc, atom.Body)
	}

	if root == nil {
		root = doc
	}

	text := extractText(root)
	text = collapseWhitespace(text)

	return &scraper.Document{
		Text: strings.TrimSpace(text),
	}, nil
}

// blockTags is the set of elements that should produce line breaks when rendered as text.
var blockTags = map[atom.Atom]bool{
	atom.P:          true,
	atom.Div:        true,
	atom.Br:         true,
	atom.Hr:         true,
	atom.H1:         true,
	atom.H2:         true,
	atom.H3:         true,
	atom.H4:         true,
	atom.H5:         true,
	atom.H6:         true,
	atom.Li:         true,
	atom.Blockquote: true,
	atom.Pre:        true,
	atom.Tr:         true,
	atom.Dt:         true,
	atom.Dd:         true,
	atom.Section:    true,
	atom.Figure:     true,
	atom.Figcaption: true,
}

func extractText(n *html.Node) string {
	if n == nil {
		return ""
	}

	if n.Type == html.TextNode {
		return n.Data
	}

	var b strings.Builder

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		text := extractText(c)

		if text == "" {
			continue
		}

		if c.Type == html.ElementNode && blockTags[c.DataAtom] {
			b.WriteString("\n")
			b.WriteString(text)
			b.WriteString("\n")
		} else {
			b.WriteString(text)
		}
	}

	return b.String()
}

func findElement(n *html.Node, tag atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == tag {
		return n
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if result := findElement(c, tag); result != nil {
			return result
		}
	}

	return nil
}

func findByAttr(n *html.Node, key, val string) *html.Node {
	if n.Type == html.ElementNode {
		for _, a := range n.Attr {
			if a.Key == key && a.Val == val {
				return n
			}
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if result := findByAttr(c, key, val); result != nil {
			return result
		}
	}

	return nil
}

func removeElements(n *html.Node, tag atom.Atom) {
	var toRemove []*html.Node

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == tag {
			toRemove = append(toRemove, c)
		} else {
			removeElements(c, tag)
		}
	}

	for _, c := range toRemove {
		n.RemoveChild(c)
	}
}

func removeByAttr(n *html.Node, key, val string) {
	var toRemove []*html.Node

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && matchAttr(c, key, val) {
			toRemove = append(toRemove, c)
		} else {
			removeByAttr(c, key, val)
		}
	}

	for _, c := range toRemove {
		n.RemoveChild(c)
	}
}

func matchAttr(n *html.Node, key, val string) bool {
	for _, a := range n.Attr {
		if a.Key == key && a.Val == val {
			return true
		}
	}

	return false
}

var reBlankLines = regexp.MustCompile(`\n{3,}`)
var reSpaces = regexp.MustCompile(`[^\S\n]+`)

func collapseWhitespace(s string) string {
	s = reSpaces.ReplaceAllString(s, " ")
	s = reBlankLines.ReplaceAllString(s, "\n\n")
	return s
}
