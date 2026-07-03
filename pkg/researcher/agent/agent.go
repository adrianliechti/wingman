package agent

import (
	"context"
	_ "embed"
	"encoding/json"
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

const finalizePrompt = "The tool-call budget is exhausted. Do not request more tools. Write the final answer now using only the evidence already gathered, with inline citations to the sources you retrieved. If the evidence is incomplete, state exactly what is missing."

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
		instructions: instructions,
		tools:        tools,
		client:       c,
	}

	for {
		exhausted := s.toolCalls >= c.maxToolCalls

		opts := completeOptions
		if exhausted {
			final := *completeOptions
			final.Tools = nil
			opts = &final

			messages = append(messages, provider.UserMessage(finalizePrompt))
		}

		acc := provider.CompletionAccumulator{}
		for completion, err := range c.completer.Complete(ctx, messages, opts) {
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
		if exhausted || len(calls) == 0 {
			return &researcher.Result{Content: result.Message.Text()}, nil
		}

		remaining := c.maxToolCalls - s.toolCalls

		run := calls
		var skipped []provider.ToolCall
		if len(calls) > remaining {
			run, skipped = calls[:remaining], calls[remaining:]
		}
		s.toolCalls += len(run)

		toolMessages := s.runCalls(ctx, run)
		for _, tc := range skipped {
			toolMessages = append(toolMessages, provider.ToolMessage(tc.ID, "Error: tool-call budget exhausted; this call was not executed."))
		}

		if remaining := c.maxToolCalls - s.toolCalls; remaining > 0 && remaining <= max(2, c.maxToolCalls/5) {
			appendText(&toolMessages[len(toolMessages)-1], fmt.Sprintf("\n\n[%d tool call(s) remaining — close the most important gap, then answer]", remaining))
		}

		messages = append(messages, toolMessages...)
	}
}

type state struct {
	instructions string
	tools        map[string]tool.Provider
	client       *Client

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
		over := s.fetchedBytes >= s.client.maxTotalFetchChars
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
		provider.SystemMessage(`You extract evidence from a fetched web page for a research task. Keep the "Source:" line at the top, then list every fact relevant to the question — preserve exact figures, dates, proper names, and short verbatim quotes where the wording matters. Keep any trailing truncation notice verbatim. Drop navigation, ads, boilerplate, and unrelated sections. If nothing on the page is relevant, reply exactly: Not relevant: <one-line reason>.`),
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

func appendText(m *provider.Message, text string) {
	for i := range m.Content {
		if r := m.Content[i].ToolResult; r != nil && len(r.Parts) > 0 {
			r.Parts[len(r.Parts)-1].Text += text
			return
		}
	}
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
