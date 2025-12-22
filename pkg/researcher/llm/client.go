package llm

import (
	"context"
	_ "embed"
	"encoding/json"
	"maps"
	"slices"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/pkg/template"
	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/adrianliechti/wingman/pkg/tool/scrape"
	"github.com/adrianliechti/wingman/pkg/tool/search"
)

var _ researcher.Provider = &Client{}

var (
	//go:embed agent.md
	agent string
)

type Client struct {
	completer provider.Completer

	searcher searcher.Provider
	scraper  scraper.Provider

	effort    provider.Effort
	verbosity provider.Verbosity

	prompt *template.Template
}

func New(completer provider.Completer, searcher searcher.Provider, options ...Option) (*Client, error) {
	prompt, err := template.NewTemplate(agent)

	if err != nil {
		return nil, err
	}

	c := &Client{
		completer: completer,
		searcher:  searcher,

		effort:    provider.EffortMinimal,
		verbosity: provider.VerbosityMedium,

		prompt: prompt,
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

func (c *Client) Research(ctx context.Context, instructions string, options *researcher.ResearchOptions) (*researcher.Result, error) {
	if options == nil {
		options = &researcher.ResearchOptions{}
	}

	prompt, err := c.prompt.Execute(map[string]any{
		"Goal":       instructions,
		"HasScraper": c.scraper != nil,
	})

	if err != nil {
		return nil, err
	}

	searchProvider, _ := search.New(c.searcher)

	tools := make(map[string]tool.Provider)
	toolDefs := make(map[string]provider.Tool)

	searchTools, _ := searchProvider.Tools(ctx)

	for _, t := range searchTools {
		tools[t.Name] = searchProvider
		toolDefs[t.Name] = t
	}

	if c.scraper != nil {
		scrapeProvider, _ := scrape.New(c.scraper)
		scrapeTools, _ := scrapeProvider.Tools(ctx)

		for _, t := range scrapeTools {
			tools[t.Name] = scrapeProvider
			toolDefs[t.Name] = t
		}
	}

	messages := []provider.Message{
		provider.SystemMessage(prompt),
		provider.UserMessage(instructions),
	}

	completeOptions := &provider.CompleteOptions{
		Tools:     slices.Collect(maps.Values(toolDefs)),
		Effort:    c.effort,
		Verbosity: c.verbosity,
	}

	for {
		acc := provider.CompletionAccumulator{}

		for completion, err := range c.completer.Complete(ctx, messages, completeOptions) {
			if err != nil {
				return nil, err
			}

			acc.Add(*completion)
		}

		result := acc.Result()

		if result.Message == nil {
			return &researcher.Result{Content: ""}, nil
		}

		messages = append(messages, *result.Message)

		calls := result.Message.ToolCalls()

		if len(calls) == 0 {
			return &researcher.Result{Content: result.Message.Text()}, nil
		}

		for _, tc := range calls {
			t, found := tools[tc.Name]

			if !found {
				messages = append(messages, provider.ToolMessage(tc.ID, "Error: unknown tool"))
				continue
			}

			var params map[string]any

			if err := json.Unmarshal([]byte(tc.Arguments), &params); err != nil {
				messages = append(messages, provider.ToolMessage(tc.ID, "Error: invalid arguments"))
				continue
			}

			println("Execute", tc.Name, tc.Arguments)

			result, err := t.Execute(ctx, tc.Name, params)

			if err != nil {
				messages = append(messages, provider.ToolMessage(tc.ID, "Error: "+err.Error()))
				continue
			}

			data, err := json.Marshal(result)

			if err != nil {
				messages = append(messages, provider.ToolMessage(tc.ID, "Error: "+err.Error()))
				continue
			}

			messages = append(messages, provider.ToolMessage(tc.ID, string(data)))
		}
	}
}
