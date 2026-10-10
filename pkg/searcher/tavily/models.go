package tavily

type searchResult struct {
	Query string `json:"query"`

	Answer string `json:"answer"`

	Results []struct {
		URL string `json:"url"`

		Title   string `json:"title"`
		Content string `json:"content"`

		PublishedDate string `json:"published_date"`

		Score float64 `json:"score"`
	} `json:"results"`
}
