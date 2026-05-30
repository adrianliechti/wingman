package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

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

//go:embed agent.md
var systemPromptSource string

const (
	defaultMaxToolCalls       = 20
	defaultMaxFetchChars      = 12 * 1024
	defaultMaxTotalFetchChars = 80 * 1024
	defaultSummarizeMinChars  = 4 * 1024

	toolWebSearch = "web_search"
	toolWebFetch  = "web_fetch"
)

var ErrBudgetExceeded = errors.New("agent researcher: tool-call budget exceeded")

type Client struct {
	completer provider.Completer

	searcher   searcher.Provider
	scraper    scraper.Provider
	summarizer provider.Completer

	effort    provider.Effort
	verbosity provider.Verbosity

	maxToolCalls       int
	maxFetchChars      int
	maxTotalFetchChars int
	summarizeMinChars  int

	prompt *template.Template
}

func New(completer provider.Completer, searcher searcher.Provider, options ...Option) (*Client, error) {
	prompt, err := template.NewTemplate(systemPromptSource)
	if err != nil {
		return nil, err
	}

	c := &Client{
		completer: completer,
		searcher:  searcher,

		maxToolCalls:       defaultMaxToolCalls,
		maxFetchChars:      defaultMaxFetchChars,
		maxTotalFetchChars: defaultMaxTotalFetchChars,
		summarizeMinChars:  defaultSummarizeMinChars,

		prompt: prompt,
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

func (c *Client) Research(ctx context.Context, instructions string, options *researcher.ResearchOptions) (*researcher.Result, error) {
	prompt, err := c.prompt.Execute(map[string]any{
		"HasScraper":   c.scraper != nil,
		"MaxToolCalls": c.maxToolCalls,
	})
	if err != nil {
		return nil, err
	}

	searchProvider, err := search.New(c.searcher)
	if err != nil {
		return nil, err
	}

	tools := map[string]tool.Provider{}
	toolDefs := map[string]provider.Tool{}

	searchTools, _ := searchProvider.Tools(ctx)
	for _, t := range searchTools {
		tools[t.Name] = searchProvider
		toolDefs[t.Name] = t
	}

	if c.scraper != nil {
		scrapeProvider, err := scrape.New(c.scraper, scrape.WithMaxChars(c.maxFetchChars))
		if err != nil {
			return nil, err
		}
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
	if c.verbosity != "" {
		completeOptions.OutputOptions = &provider.OutputOptions{Verbosity: c.verbosity}
	}
	if c.effort != "" {
		completeOptions.ReasoningOptions = &provider.ReasoningOptions{Effort: c.effort}
	}

	s := &state{
		instructions:       instructions,
		tools:              tools,
		client:             c,
		maxTotalFetchChars: c.maxTotalFetchChars,
	}

	for {
		if s.toolCalls >= c.maxToolCalls {
			return nil, ErrBudgetExceeded
		}

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

		remaining := c.maxToolCalls - s.toolCalls
		if len(calls) > remaining {
			calls = calls[:remaining]
		}
		s.toolCalls += len(calls)

		toolMessages := s.runCalls(ctx, calls)
		messages = append(messages, toolMessages...)
	}
}

type state struct {
	instructions       string
	tools              map[string]tool.Provider
	client             *Client
	maxTotalFetchChars int

	mu           sync.Mutex
	toolCalls    int
	fetchedBytes int
}

func (s *state) runCalls(ctx context.Context, calls []provider.ToolCall) []provider.Message {
	results := make([]provider.Message, len(calls))

	var wg sync.WaitGroup
	for i, tc := range calls {
		i, tc := i, tc
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = s.runCall(ctx, tc)
		}()
	}
	wg.Wait()

	return results
}

func (s *state) runCall(ctx context.Context, tc provider.ToolCall) provider.Message {
	p, found := s.tools[tc.Name]
	if !found {
		return provider.ToolMessage(tc.ID, "Error: unknown tool")
	}

	if tc.Name == toolWebFetch {
		s.mu.Lock()
		over := s.fetchedBytes >= s.maxTotalFetchChars
		s.mu.Unlock()

		if over {
			return provider.ToolMessage(tc.ID, "Error: total fetch budget exceeded; rely on already-fetched evidence and finalize the answer")
		}
	}

	var params map[string]any
	if err := json.Unmarshal([]byte(tc.Arguments), &params); err != nil {
		return provider.ToolMessage(tc.ID, "Error: invalid arguments")
	}

	value, err := p.Execute(ctx, tc.Name, params)
	if err != nil {
		return provider.ToolMessage(tc.ID, "Error: "+err.Error())
	}

	text := renderResult(p, tc.Name, value)

	if tc.Name == toolWebFetch {
		s.mu.Lock()
		s.fetchedBytes += len(text)
		s.mu.Unlock()

		if s.client.summarizer != nil && len(text) >= s.client.summarizeMinChars {
			if summary := s.client.summarize(ctx, s.instructions, text); summary != "" {
				text = summary
			}
		}
	}

	return provider.ToolMessage(tc.ID, text)
}

func (c *Client) summarize(ctx context.Context, instructions, page string) string {
	if c.summarizer == nil {
		return ""
	}

	messages := []provider.Message{
		provider.SystemMessage(`You are a precise extractor. Given a fetched web page and a research question, produce a concise extract (5-12 sentences) of facts relevant to the question. Preserve the page URL (the "Source:" line at the top). Drop boilerplate, ads, navigation, unrelated paragraphs. If the page is not relevant, say so in one sentence.`),
		provider.UserMessage(fmt.Sprintf("Research question:\n%s\n\nPage:\n%s", instructions, page)),
	}

	acc := provider.CompletionAccumulator{}
	for completion, err := range c.summarizer.Complete(ctx, messages, nil) {
		if err != nil {
			return ""
		}
		acc.Add(*completion)
	}
	msg := acc.Result().Message
	if msg == nil {
		return ""
	}
	return msg.Text()
}

func renderResult(p tool.Provider, name string, value any) string {
	if r, ok := p.(tool.Resulter); ok {
		res := r.Result(name, value)
		if len(res.Parts) > 0 && res.Parts[0].Text != "" {
			return res.Parts[0].Text
		}
	}
	if s, ok := value.(string); ok {
		return s
	}
	data, _ := json.Marshal(value)
	return string(data)
}
