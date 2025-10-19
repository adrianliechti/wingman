package perplexity

import (
	"context"
	"regexp"
	"strings"

	"github.com/adrianliechti/wingman/pkg/researcher"

	"github.com/openai/openai-go/v3"
)

var _ researcher.Provider = &Client{}

type Client struct {
	*Config
	completions openai.ChatCompletionService
}

func New(token string, options ...Option) (*Client, error) {
	cfg := &Config{
		token: token,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Client{
		Config:      cfg,
		completions: openai.NewChatCompletionService(cfg.Options()...),
	}, nil
}

func (c *Client) Research(ctx context.Context, instructions string, options *researcher.ResearchOptions) (*researcher.Result, error) {
	if options == nil {
		options = new(researcher.ResearchOptions)
	}

	model := c.model

	if model == "" {
		model = "sonar-deep-research"
	}

	body := openai.ChatCompletionNewParams{
		Model: openai.ChatModel(model),

		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage(instructions),
		},
	}

	completion, err := c.completions.New(ctx, body)

	if err != nil {
		return nil, err
	}

	content := strings.TrimSpace(removeTags(completion.Choices[0].Message.Content))

	return &researcher.Result{
		Content: content,
	}, nil
}

func removeTags(text string) string {
	re := regexp.MustCompile(`(?s)<[a-zA-Z][a-zA-Z0-9]*>.*?</[a-zA-Z][a-zA-Z0-9]*>`)
	return re.ReplaceAllString(text, "")
}
