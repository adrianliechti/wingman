package client

import (
	"net/http"
)

type Client struct {
	Models ModelService

	Embeddings  EmbeddingService
	Completions CompletionService

	Syntheses      SynthesisService
	Renderings     RenderingService
	Transcriptions TranscriptionService

	Scrapes    ScrapeService
	Searches   SearchService
	Researches ResearchService

	Segments    SegmentService
	Extractions ExtractionService

	Summaries SummaryService
}

func New(url string, opts ...RequestOption) *Client {
	opts = append(opts, WithURL(url))

	return &Client{
		Models: NewModelService(opts...),

		Embeddings:  NewEmbeddingService(opts...),
		Completions: NewCompletionService(opts...),

		Syntheses:      NewSynthesisService(opts...),
		Renderings:     NewRenderingService(opts...),
		Transcriptions: NewTranscriptionService(opts...),

		Scrapes:    NewScrapeService(opts...),
		Searches:   NewSearchService(opts...),
		Researches: NewResearchService(opts...),

		Segments:    NewSegmentService(opts...),
		Extractions: NewExtractionService(opts...),

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

func Ptr[T any](v T) *T {
	return &v
}
