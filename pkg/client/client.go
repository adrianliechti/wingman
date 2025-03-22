package client

import (
	"net/http"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

type Client struct {
	Models openai.ModelService

	Embeddings  openai.EmbeddingService
	Completions openai.ChatCompletionService

	Segments    SegmentService
	Extractions ExtractionService

	Documents DocumentService
	Summaries SummaryService
}

func New(url string, opts ...RequestOption) *Client {
	opts = append(opts, WithURL(url))

	return &Client{
		Models: openai.NewModelService(openaiOptions(opts...)...),

		Embeddings:  openai.NewEmbeddingService(openaiOptions(opts...)...),
		Completions: openai.NewChatCompletionService(openaiOptions(opts...)...),

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

func openaiOptions(opts ...RequestOption) []option.RequestOption {
	c := newRequestConfig(opts...)

	options := []option.RequestOption{
		option.WithHTTPClient(c.Client),
		option.WithBaseURL(strings.TrimRight(c.URL, "/") + "/v1/"),
	}

	if c.Token != "" {
		options = append(options, option.WithAPIKey(c.Token))
	}

	return options
}
