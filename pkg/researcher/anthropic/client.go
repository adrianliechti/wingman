package anthropic

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/adrianliechti/wingman/pkg/researcher"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

var _ researcher.Provider = &Client{}

// webToolsBeta enables dynamic filtering: Claude post-processes web search
// results with the code execution tool before they enter the context window.
const webToolsBeta anthropic.AnthropicBeta = "code-execution-web-tools-2026-02-09"

// maxContinuations bounds pause_turn resumptions; each one restarts the
// server-side tool loop and its max_uses budget.
const maxContinuations = 5

type Client struct {
	*Config
	messages anthropic.BetaMessageService
}

func New(token string, options ...Option) (*Client, error) {
	cfg := &Config{
		token: token,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Client{
		Config:   cfg,
		messages: anthropic.NewBetaMessageService(cfg.Options()...),
	}, nil
}

func (c *Client) Research(ctx context.Context, instructions string, options *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
	return func(yield func(*researcher.Result, error) bool) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		if !yield(&researcher.Result{Progress: "Starting research"}, nil) {
			return
		}
		model := c.model

		if model == "" {
			model = "claude-sonnet-4-6"
		}

		body := anthropic.BetaMessageNewParams{
			Model: anthropic.Model(model),

			// The budget covers the server-side tool loop as well as the report.
			MaxTokens: 64000,

			Betas: []anthropic.AnthropicBeta{webToolsBeta},

			Messages: []anthropic.BetaMessageParam{
				anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(instructions)),
			},

			System: []anthropic.BetaTextBlockParam{
				{Text: "Research the user's question using web sources. Honor the requested timeframes, document versions, and source restrictions. Prefer primary sources and cite their URLs. If the evidence is insufficient, say so. Return a self-contained report that follows the requested length and format."},
			},

			Tools: []anthropic.BetaToolUnionParam{
				// Keep the basic fetch tool: with web_fetch_20260209, Sonnet 4.6
				// filtered fetched pages in a long code-execution loop (>240s for
				// a two-page comparison that otherwise takes ~13s).
				{
					OfWebFetchTool20250910: &anthropic.BetaWebFetchTool20250910Param{
						MaxUses:   anthropic.Int(5),
						Citations: anthropic.BetaCitationsConfigParam{Enabled: anthropic.Bool(true)},
					},
				},
				{
					OfWebSearchTool20260209: &anthropic.BetaWebSearchTool20260209Param{
						MaxUses: anthropic.Int(5),
					},
				},
				{
					OfCodeExecutionTool20260120: &anthropic.BetaCodeExecutionTool20260120Param{},
				},
			},
		}

		var documents []source
		for continuation := 0; ; continuation++ {
			var message anthropic.BetaMessage
			stopped := false
			lastProgress := ""
			// Close each turn before starting a continuation, including early exits.
			continued := func() bool {
				stream := c.messages.NewStreaming(ctx, body)
				defer stream.Close()
				for stream.Next() {
					event := stream.Current()
					if err := message.Accumulate(event); err != nil {
						yield(nil, err)
						return false
					}
					progress := ""
					switch event.Type {
					case "message_start":
						progress = "Planning research"
					case "content_block_start":
						block := event.ContentBlock
						switch block.Type {
						case "thinking", "redacted_thinking":
							progress = "Analyzing research evidence"
						case "text":
							progress = "Writing research report"
						case "server_tool_use":
							switch block.Name {
							case "web_search":
								progress = "Searching the web"
							case "web_fetch":
								progress = "Reading web sources"
							default:
								progress = "Analyzing sources with " + block.Name
							}
						case "web_search_tool_result":
							result := block.AsWebSearchToolResult()
							if result.Content.ErrorCode != "" {
								progress = "Web search failed: " + string(result.Content.ErrorCode)
							} else {
								progress = fmt.Sprintf("Web search returned %d sources", len(result.Content.OfBetaWebSearchResultBlockArray))
							}
						case "web_fetch_tool_result":
							result := block.AsWebFetchToolResult().Content
							if result.ErrorCode != "" {
								progress = "Source retrieval failed: " + string(result.ErrorCode)
							} else {
								documents = append(documents, source{url: result.URL, title: result.Content.Title})
								progress = "Retrieved source: " + result.URL
							}
						case "code_execution_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result":
							progress = "Source analysis complete"
						}
					case "content_block_stop":
						block := message.Content[event.Index]
						if block.Type == "server_tool_use" {
							var input struct {
								Query string `json:"query"`
								URL   string `json:"url"`
							}
							if json.Unmarshal(block.Input, &input) == nil {
								if block.Name == "web_search" && input.Query != "" {
									progress = "Searching the web: " + input.Query
								}
								if block.Name == "web_fetch" && input.URL != "" {
									progress = "Reading source: " + input.URL
								}
							}
						}
					case "message_stop":
						stopped = true
					}
					if progress != "" && progress != lastProgress {
						lastProgress = progress
						if !yield(&researcher.Result{Progress: progress}, nil) {
							return false
						}
					}
				}
				if err := stream.Err(); err != nil {
					yield(nil, err)
					return false
				}
				if !stopped {
					yield(nil, errors.New("anthropic research: stream ended before message_stop"))
					return false
				}
				return true
			}()
			if !continued {
				return
			}
			if message.StopReason == "pause_turn" {
				if continuation == maxContinuations {
					yield(nil, fmt.Errorf("anthropic research incomplete: still paused after %d continuations", maxContinuations))
					return
				}
				if !yield(&researcher.Result{Progress: "Continuing research after paused turn"}, nil) {
					return
				}
				body.Messages = append(body.Messages, message.ToParam())
				if message.Container.ID != "" {
					body.Container = anthropic.BetaMessageNewParamsContainerUnion{OfString: anthropic.String(message.Container.ID)}
				}
				continue
			}
			if message.StopReason == "max_tokens" || message.StopReason == "tool_use" {
				yield(nil, fmt.Errorf("anthropic research incomplete: %s", message.StopReason))
				return
			}
			yield(&researcher.Result{Content: outputText(&message, documents...)}, nil)
			return
		}
	}
}

func outputText(message *anthropic.BetaMessage, documents ...source) string {
	var content strings.Builder
	var sources []source
	seen := map[string]struct{}{}

	// Text before a server tool is research commentary, rather than the report.
	start := 0
	for i, block := range message.Content {
		switch block.Type {
		case "server_tool_use", "web_search_tool_result", "web_fetch_tool_result", "code_execution_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result", "thinking", "redacted_thinking":
			start = i + 1
		}
	}
	for _, c := range message.Content[start:] {
		if c.Type == "text" {
			content.WriteString(c.Text)

			for _, citation := range c.Citations {
				var cited source
				switch citation.Type {
				case "web_search_result_location":
					cited = source{url: citation.URL, title: citation.Title}
				case "char_location", "page_location", "content_block_location":
					if citation.DocumentIndex >= 0 && citation.DocumentIndex < int64(len(documents)) {
						cited = documents[citation.DocumentIndex]
					}
				}
				if cited.url == "" {
					continue
				}

				if _, ok := seen[cited.url]; ok {
					continue
				}

				seen[cited.url] = struct{}{}
				sources = append(sources, cited)
			}
		}
	}

	return appendSources(strings.TrimSpace(content.String()), sources)
}

type source struct {
	title string
	url   string
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
