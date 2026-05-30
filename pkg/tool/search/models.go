package search

type Result struct {
	URL     string `json:"url"`
	Title   string `json:"title,omitempty"`
	Snippet string `json:"snippet,omitempty"`
	Content string `json:"content,omitempty"`
}
