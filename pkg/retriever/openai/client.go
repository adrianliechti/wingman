package openai

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/retriever"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

var _ retriever.Provider = &Client{}

type Client struct {
	*Config
	responses responses.ResponseService
}

func New(token string, options ...Option) (*Client, error) {
	cfg := &Config{
		token: token,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Client{
		Config:    cfg,
		responses: responses.NewResponseService(cfg.Options()...),
	}, nil
}

func (c *Client) Retrieve(ctx context.Context, query string, options *retriever.RetrieveOptions) ([]retriever.Result, error) {
	if options == nil {
		options = new(retriever.RetrieveOptions)
	}

	model := c.model

	if model == "" {
		model = "gpt-5"
	}

	input := "Find relevant information for the following query: " + query

	body := responses.ResponseNewParams{
		Model: responses.ResponsesModel(model),

		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(input),
		},

		Tools: []responses.ToolUnionParam{
			{
				OfWebSearch: &responses.WebSearchToolParam{
					Type: responses.WebSearchToolTypeWebSearch,
				},
			},
		},
	}

	response, err := c.responses.New(ctx, body)

	if err != nil {
		return nil, err
	}

	var result []retriever.Result

	if val := response.OutputText(); val != "" {
		result = append(result, retriever.Result{
			Content: val,
		})
	}

	for _, item := range response.Output {
		for _, content := range item.Content {

			for _, a := range content.Annotations {
				if a.URL == "" {
					continue
				}

				result = append(result, retriever.Result{
					Source: a.URL,

					Title: a.Title,
				})
			}
		}
	}

	return result, nil
}
