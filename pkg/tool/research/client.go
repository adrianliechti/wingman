package research

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/adrianliechti/wingman/pkg/tool"
)

const ToolName = "web_research"

var (
	_ tool.Provider = (*Client)(nil)
	_ tool.Resulter = (*Client)(nil)
)

type Client struct {
	provider researcher.Provider
}

func New(provider researcher.Provider, options ...Option) (*Client, error) {
	if provider == nil {
		return nil, errors.New("research: missing researcher provider")
	}

	c := &Client{
		provider: provider,
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

func (c *Client) Tools(ctx context.Context) ([]tool.Tool, error) {
	return []tool.Tool{
		{
			Name:        ToolName,
			Description: "Delegate a self-contained web research question requiring several searches, source comparisons, or a cited report. Returns the configured researcher's report; check its evidence before drawing further conclusions. May take seconds to minutes. When available, use web_search for targeted lookups and web_fetch to read a supplied URL.",

			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"instructions": map[string]any{
						"type":        "string",
						"description": "A clear, self-contained description of what to research. Include the question, any constraints (timeframe, sources to prefer), and the shape of the answer you want.",
					},
				},
				"required": []string{"instructions"},
			},
		},
	}, nil
}

func (c *Client) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
	if name != ToolName {
		return nil, tool.ErrInvalidTool
	}

	instructions, err := tool.StringParameter(parameters, "instructions")
	if err != nil {
		return nil, fmt.Errorf("research: %w", err)
	}
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return nil, errors.New("research: missing instructions parameter")
	}

	data, err := c.provider.Research(ctx, instructions, &researcher.ResearchOptions{})
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, errors.New("research: empty response")
	}

	return data.Content, nil
}

// Result implements tool.Resulter so the agent chain sees the research report
// as plain markdown text instead of a JSON-quoted blob.
func (c *Client) Result(name string, value any) provider.ToolResult {
	text, _ := value.(string)
	return tool.TextResult(text)
}
