package scrape

import "time"

type Result struct {
	URL         string    `json:"url"`
	Title       string    `json:"title,omitempty"`
	RetrievedAt time.Time `json:"retrieved_at"`
	Text        string    `json:"text"`
}
