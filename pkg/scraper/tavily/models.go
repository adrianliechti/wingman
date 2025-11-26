package tavily

type extractResult struct {
	Results []struct {
		URL string `json:"url"`

		Content string `json:"raw_content"`
	} `json:"results"`
}
