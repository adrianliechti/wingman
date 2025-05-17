package exa

type SearchRequest struct {
	Query string `json:"query"`

	Contents SearchContents `json:"contents,omitempty"`
}

type SearchContents struct {
	Text bool `json:"text,omitempty"`

	LiveCrawl LiveCrawl `json:"livecrawl,omitempty"`
}

type LiveCrawl string

const (
	LiveCrawlAuto     LiveCrawl = "auto"
	LiveCrawlAlways   LiveCrawl = "always"
	LiveCrawlNever    LiveCrawl = "never"
	LiveCrawlFallback LiveCrawl = "fallback"
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
