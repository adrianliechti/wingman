package openai

import (
	"context"
	"strings"

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
		model = "gpt-5.5"
	}

	tool := responses.ToolUnionParam{
		OfWebSearch: &responses.WebSearchToolParam{
			Type: responses.WebSearchToolTypeWebSearch,
		},
	}

	if strings.Contains(model, "deep-research") {
		tool = responses.ToolUnionParam{
			OfWebSearchPreview: &responses.WebSearchPreviewToolParam{
				Type: responses.WebSearchPreviewToolTypeWebSearchPreview,
			},
		}
	}

	if tool.OfWebSearch != nil {
		tool.OfWebSearch.SearchContextSize = responses.WebSearchToolSearchContextSizeHigh
	}

	if tool.OfWebSearchPreview != nil {
		tool.OfWebSearchPreview.SearchContextSize = responses.WebSearchPreviewToolSearchContextSizeHigh
	}

	body := responses.ResponseNewParams{
		Model: responses.ResponsesModel(model),

		Instructions: openai.String("Use web search to gather current, source-backed information. Include citations in the final answer."),

		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(instructions),
		},

		Include: []responses.ResponseIncludable{
			responses.ResponseIncludableWebSearchCallActionSources,
		},

		Tools: []responses.ToolUnionParam{tool},
	}

	body.MaxToolCalls = openai.Int(10)

	response, err := c.responses.New(ctx, body)

	if err != nil {
		return nil, err
	}

	return &researcher.Result{
		Content: outputText(response),
	}, nil
}

type source struct {
	title string
	url   string
}

func outputText(response *responses.Response) string {
	var content strings.Builder

	var sources []source
	seen := map[string]struct{}{}

	for _, item := range response.Output {
		for _, c := range item.Content {
			if c.Type != "output_text" {
				continue
			}

			content.WriteString(c.Text)

			for _, a := range c.Annotations {
				if a.Type != "url_citation" || a.URL == "" {
					continue
				}

				if _, ok := seen[a.URL]; ok {
					continue
				}

				seen[a.URL] = struct{}{}
				sources = append(sources, source{
					title: a.Title,
					url:   a.URL,
				})
			}
		}
	}

	return appendSources(strings.TrimSpace(content.String()), sources)
}

func appendSources(content string, sources []source) string {
	if len(sources) == 0 {
		return content
	}

	var b strings.Builder

	if content != "" {
		b.WriteString(content)
		b.WriteString("\n\n")
	}

	b.WriteString("Sources:")

	for _, source := range sources {
		b.WriteString("\n- ")

		if source.title != "" {
			b.WriteString(source.title)
			b.WriteString(": ")
		}

		b.WriteString(source.url)
	}

	return b.String()
}
