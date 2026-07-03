package exa

type SearchRequest struct {
	Query string `json:"query"`

	Type string `json:"type,omitempty"`

	Category string `json:"category,omitempty"`
	Location string `json:"userLocation,omitempty"`

	NumResults *int `json:"numResults,omitempty"`

	IncludeDomains []string `json:"includeDomains,omitempty"`
	ExcludeDomains []string `json:"excludeDomains,omitempty"`

	Contents *SearchContents `json:"contents"`
}

type SearchContents struct {
	Text bool `json:"text,omitempty"`
}

type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

type SearchResult struct {
	ID string `json:"id"`

	URL   string `json:"url"`
	Title string `json:"title"`

	PublishedDate string `json:"publishedDate"`

	Text string `json:"text"`
}
