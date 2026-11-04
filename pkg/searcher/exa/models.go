package exa

type SearchRequest struct {
	Query string `json:"query"`

	Type string `json:"type,omitzero"`

	Category string `json:"category,omitzero"`
	Location string `json:"userLocation,omitzero"`

	StartPublishedDate string `json:"startPublishedDate,omitzero"`
	EndPublishedDate   string `json:"endPublishedDate,omitzero"`

	NumResults *int `json:"numResults,omitzero"`

	IncludeDomains []string `json:"includeDomains,omitempty"`
	ExcludeDomains []string `json:"excludeDomains,omitempty"`

	Contents *SearchContents `json:"contents"`
}

type SearchContents struct {
	Text       bool              `json:"text,omitzero"`
	Highlights *SearchHighlights `json:"highlights,omitzero"`
}

type SearchHighlights struct {
	Dynamic bool `json:"dynamic"`
}

type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

type SearchResult struct {
	ID string `json:"id"`

	URL   string `json:"url"`
	Title string `json:"title"`

	PublishedDate string `json:"publishedDate"`

	Text       string   `json:"text"`
	Highlights []string `json:"highlights"`
}
