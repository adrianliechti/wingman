package retrieve

type Result struct {
	Title   string `json:"title,omitempty"`
	Source  string `json:"source,omitempty"`
	Content string `json:"content,omitempty"`
}
