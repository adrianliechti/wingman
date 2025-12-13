package exa

type SearchRequest struct {
	Query string `json:"query"`

	IncludeDomains []string `json:"includeDomains,omitempty"`

	Contents SearchContents `json:"contents"`
}

type SearchContents struct {
	Text bool `json:"text,omitempty"`

	LiveCrawl LiveCrawl `json:"livecrawl,omitempty"`
}

type LiveCrawl string

const (
	LiveCrawlNever     LiveCrawl = "never"
	LiveCrawlAlways    LiveCrawl = "always"
	LiveCrawlFallback  LiveCrawl = "fallback"
	LiveCrawlPreferred LiveCrawl = "preferred"
)

type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

type SearchResult struct {
	ID string `json:"id"`

	URL   string `json:"url"`
	Title string `json:"title"`

	Text string `json:"text"`
}
