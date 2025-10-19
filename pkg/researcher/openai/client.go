package openai

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/researcher"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

var _ researcher.Provider = &Client{}

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

func (c *Client) Research(ctx context.Context, instructions string, options *researcher.ResearchOptions) (*researcher.Result, error) {
	if options == nil {
		options = new(researcher.ResearchOptions)
	}

	model := c.model

	if model == "" {
		model = "gpt-5"
	}

	body := responses.ResponseNewParams{
		Model: responses.ResponsesModel(model),

		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(instructions),
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

	return &researcher.Result{
		Content: response.OutputText(),
	}, nil
}
