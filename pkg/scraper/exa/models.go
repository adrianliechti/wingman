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
	Results  []ContentsResult `json:"results"`
	Statuses []ContentsStatus `json:"statuses,omitempty"`
}

type ContentsResult struct {
	ID string `json:"id"`

	URL   string `json:"url"`
	Title string `json:"title"`

	Text string `json:"text"`
}

type ContentsStatus struct {
	ID     string             `json:"id"`
	Status string             `json:"status"`
	Source string             `json:"source,omitempty"`
	Error  *ContentsStatusErr `json:"error,omitempty"`
}

type ContentsStatusErr struct {
	Tag         string `json:"tag,omitempty"`
	HTTPStatus  int    `json:"httpStatusCode,omitempty"`
}
