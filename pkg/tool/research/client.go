package research

import (
	"context"
	"encoding/json"
	"errors"
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
			Description: "Run a deep, multi-step research investigation and return a synthesized, cited answer. SLOW (tens of seconds to minutes) — only use this for complex questions that need cross-referenced sources, multi-hop reasoning, or in-depth analysis. For quick lookups, recent facts, or a single fact-check, prefer `web_search` (and `web_fetch` for a specific URL) — they are orders of magnitude faster.",

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

	instructions, _ := parameters["instructions"].(string)
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return nil, errors.New("research: missing instructions parameter")
	}

	data, err := c.provider.Research(ctx, instructions, &researcher.ResearchOptions{})
	if err != nil {
		return nil, err
	}

	return data.Content, nil
}

func (c *Client) Result(name string, value any) provider.ToolResult {
	switch v := value.(type) {
	case string:
		return provider.ToolResult{Parts: []provider.Part{{Text: v}}}
	case Result:
		return provider.ToolResult{Parts: []provider.Part{{Text: v.Content}}}
	default:
		data, _ := json.Marshal(value)
		return provider.ToolResult{Parts: []provider.Part{{Text: string(data)}}}
	}
}
