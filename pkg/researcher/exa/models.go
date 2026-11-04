package exa

import "encoding/json/jsontext"

// Research is served by /search with the deep-reasoning type.

type SearchRequest struct {
	Query string `json:"query"`

	Type         string `json:"type,omitzero"`
	Stream       bool   `json:"stream,omitzero"`
	SystemPrompt string `json:"systemPrompt,omitzero"`

	OutputSchema *OutputSchema `json:"outputSchema,omitzero"`
}

type OutputSchema struct {
	Type        string `json:"type"`
	Description string `json:"description,omitzero"`
}

type SearchResponse struct {
	Output *SearchOutput `json:"output,omitzero"`
}

type SearchOutput struct {
	// Content is a plain JSON string when outputSchema.type is "text".
	Content jsontext.Value `json:"content"`

	Grounding []Grounding `json:"grounding,omitempty"`
}

type Grounding struct {
	Field string `json:"field,omitzero"`

	Citations []Citation `json:"citations,omitempty"`

	Confidence string `json:"confidence,omitzero"`
}

type Citation struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}
