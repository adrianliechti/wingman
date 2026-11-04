package tavily

import (
	"encoding/json/jsontext"

	"github.com/adrianliechti/wingman/server/web"
)

type SearchRequest struct {
	web.Selection
	Query                string         `json:"query"`
	MaxResults           *int           `json:"max_results"`
	Topic                string         `json:"topic"`
	Country              string         `json:"country"`
	Include              []string       `json:"include_domains"`
	Exclude              []string       `json:"exclude_domains"`
	Start                string         `json:"start_date"`
	End                  string         `json:"end_date"`
	TimeRange            string         `json:"time_range"`
	Days                 *int           `json:"days"`
	IncludePublishedDate bool           `json:"include_published_date"`
	IncludeRawContent    jsontext.Value `json:"include_raw_content"`
}

type SearchResult struct {
	URL           string   `json:"url"`
	Title         string   `json:"title"`
	Content       string   `json:"content"`
	Score         *float64 `json:"score,omitzero"`
	RawContent    *string  `json:"raw_content"`
	PublishedDate string   `json:"published_date,omitzero"`
}

type ExtractRequest struct {
	web.Selection
	URLs    web.URLList `json:"urls"`
	Timeout *float64    `json:"timeout"`
}

type SearchResponse struct {
	Query        string         `json:"query"`
	Answer       *string        `json:"answer"`
	Images       []string       `json:"images"`
	Results      []SearchResult `json:"results"`
	ResponseTime float64        `json:"response_time"`
	RequestID    string         `json:"request_id"`
}
type ExtractResult struct {
	URL        string   `json:"url"`
	RawContent string   `json:"raw_content"`
	Images     []string `json:"images"`
}
type FailedResult struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}
type ExtractResponse struct {
	Results       []ExtractResult `json:"results"`
	FailedResults []FailedResult  `json:"failed_results"`
	ResponseTime  float64         `json:"response_time"`
	RequestID     string          `json:"request_id"`
}
