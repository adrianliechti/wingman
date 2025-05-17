package exa

type ContentsRequest struct {
	URLs []string `json:"urls"`

	LiveCrawl LiveCrawl `json:"livecrawl,omitempty"`
}

type LiveCrawl string

const (
	LiveCrawlAuto     LiveCrawl = "auto"
	LiveCrawlAlways   LiveCrawl = "always"
	LiveCrawlNever    LiveCrawl = "never"
	LiveCrawlFallback LiveCrawl = "fallback"
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
