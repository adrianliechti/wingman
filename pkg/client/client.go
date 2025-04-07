package client

import (
	"net/http"
)

type Client struct {
	Models ModelService

	Embeddings  EmbeddingService
	Completions CompletionService

	Syntheses  SynthesisService
	Renderings RenderingService

	Segments    SegmentService
	Extractions ExtractionService

	Documents DocumentService
	Summaries SummaryService
}

func New(url string, opts ...RequestOption) *Client {
	opts = append(opts, WithURL(url))

	return &Client{
		Models: NewModelService(opts...),

		Embeddings:  NewEmbeddingService(opts...),
		Completions: NewCompletionService(opts...),

		Syntheses:  NewSynthesisService(opts...),
		Renderings: NewRenderingService(opts...),

		Segments:    NewSegmentService(opts...),
		Extractions: NewExtractionService(opts...),

		Documents: NewDocumentService(opts...),
		Summaries: NewSummaryService(opts...),
	}
}

func newRequestConfig(opts ...RequestOption) *RequestConfig {
	c := &RequestConfig{
		Client: http.DefaultClient,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}
