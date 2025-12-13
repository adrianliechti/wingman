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

	prompt *template.Template
}

func New(completer provider.Completer, searcher searcher.Provider, scraper scraper.Provider) (*Client, error) {
	prompt, err := template.NewTemplate(agent)

	if err != nil {
		return nil, err
	}

	return &Client{
		completer: completer,
		searcher:  searcher,
		scraper:   scraper,

		prompt: prompt,
	}, nil
}

// Research implements the main agent loop for deep research
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
		Tools: slices.Collect(maps.Values(toolDefs)),
	}

	for {
		completion, err := c.completer.Complete(ctx, messages, completeOptions)

		if err != nil {
			return nil, err
		}

		if completion.Message == nil {
			return &researcher.Result{Content: ""}, nil
		}

		messages = append(messages, *completion.Message)

		calls := completion.Message.ToolCalls()

		if len(calls) == 0 {
			return &researcher.Result{Content: completion.Message.Text()}, nil
		}

		for _, c := range calls {
			t, found := tools[c.Name]

			if !found {
				messages = append(messages, provider.ToolMessage(c.ID, "Error: unknown tool"))
				continue
			}

			var params map[string]any

			if err := json.Unmarshal([]byte(c.Arguments), &params); err != nil {
				messages = append(messages, provider.ToolMessage(c.ID, "Error: invalid arguments"))
				continue
			}

			println("Executing", c.Name, c.Arguments)

			result, err := t.Execute(ctx, c.Name, params)

			if err != nil {
				messages = append(messages, provider.ToolMessage(c.ID, "Error: "+err.Error()))
				continue
			}

			data, err := json.Marshal(result)

			if err != nil {
				messages = append(messages, provider.ToolMessage(c.ID, "Error: "+err.Error()))
				continue
			}

			messages = append(messages, provider.ToolMessage(c.ID, string(data)))
		}
	}
}
