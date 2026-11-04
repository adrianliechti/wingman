package exa

import (
	"encoding/json/jsontext"

	"github.com/adrianliechti/wingman/server/web"
)

type SearchRequest struct {
	web.Selection
	Query      string          `json:"query"`
	NumResults *int            `json:"numResults"`
	Category   string          `json:"category"`
	Location   string          `json:"userLocation"`
	Include    []string        `json:"includeDomains"`
	Exclude    []string        `json:"excludeDomains"`
	Start      string          `json:"startPublishedDate"`
	End        string          `json:"endPublishedDate"`
	Contents   *ContentOptions `json:"contents"`
}

type Result struct {
	ID            string   `json:"id"`
	URL           string   `json:"url"`
	Title         string   `json:"title,omitzero"`
	PublishedDate string   `json:"publishedDate,omitzero"`
	Text          *string  `json:"text,omitzero"`
	Highlights    []string `json:"highlights,omitempty"`
}

type ContentOptions struct {
	Text       jsontext.Value `json:"text"`
	Highlights jsontext.Value `json:"highlights"`
}

type ContentsRequest struct {
	web.Selection
	URLs web.URLList    `json:"urls"`
	IDs  web.URLList    `json:"ids"`
	Text jsontext.Value `json:"text"`
}

type Status struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  *struct {
		Tag string `json:"tag"`
	} `json:"error,omitzero"`
}

type Response struct {
	RequestID  string   `json:"requestId"`
	Results    []Result `json:"results"`
	Statuses   []Status `json:"statuses,omitempty"`
	SearchTime float64  `json:"searchTime,omitzero"`
}
