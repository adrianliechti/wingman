package exa

type ContentsRequest struct {
	URLs []string `json:"urls"`

	Text bool `json:"text"`

	SubPages int `json:"subpages,omitempty"`

	LiveCrawl LiveCrawl `json:"livecrawl,omitempty"`
}

type LiveCrawl string

const (
	LiveCrawlNever     LiveCrawl = "never"
	LiveCrawlAlways    LiveCrawl = "always"
	LiveCrawlFallback  LiveCrawl = "fallback"
	LiveCrawlPreferred LiveCrawl = "preferred"
)

type ContentsResponse struct {
	Results []ContentsResult `json:"results"`
}

type ContentsResult struct {
	ID string `json:"id"`

	URL   string `json:"url"`
	Title string `json:"title"`

	Text string `json:"text"`
}
