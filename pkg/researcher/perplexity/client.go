package perplexity

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"iter"
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
			model = "sonar-deep-research"
		}
		stream := c.completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
			Model: openai.ChatModel(model),
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.SystemMessage("Honor the requested timeframes, document versions, and source restrictions. Cite source URLs, distinguish uncertainty, and follow the requested report length and format."),
				openai.UserMessage(instructions),
			},
		})
		defer stream.Close()
		var content strings.Builder
		var citations []string
		var sourceOrder []string
		titles := map[string]string{}
		seen := map[string]bool{}
		lastProgress := ""
		previousSourceCount := 0
		finished, writing := false, false
		for stream.Next() {
			chunk := stream.Current()
			var metadata struct {
				Citations     []string `json:"citations"`
				SearchResults []struct {
					URL   string `json:"url"`
					Title string `json:"title"`
				} `json:"search_results"`
			}
			if err := json.Unmarshal([]byte(chunk.RawJSON()), &metadata); err != nil {
				yield(nil, err)
				return
			}
			progress := ""
			for _, source := range metadata.SearchResults {
				if source.URL != "" {
					if _, found := titles[source.URL]; !found {
						sourceOrder = append(sourceOrder, source.URL)
					}
					seen[source.URL] = true
					titles[source.URL] = source.Title
				}
			}
			if len(metadata.Citations) > 0 {
				citations = metadata.Citations
				for _, url := range citations {
					if url != "" {
						seen[url] = true
					}
				}
			}
			if len(seen) > previousSourceCount {
				previousSourceCount = len(seen)
				progress = fmt.Sprintf("Found %d research sources", len(seen))
				if progress != lastProgress {
					lastProgress = progress
					if !yield(&researcher.Result{Progress: progress}, nil) {
						return
					}
				}
			}
			progress = ""
			for _, choice := range chunk.Choices {
				if choice.Index != 0 {
					continue
				}
				var delta struct {
					ReasoningContent string `json:"reasoning_content"`
				}
				if raw := choice.Delta.RawJSON(); raw != "" {
					if err := json.Unmarshal([]byte(raw), &delta); err != nil {
						yield(nil, err)
						return
					}
				}
				if delta.ReasoningContent != "" {
					progress = "Analyzing research evidence"
				}
				if choice.Delta.Content != "" {
					content.WriteString(choice.Delta.Content)
					// Writing is final; stop rescanning the growing report on every chunk.
					if !writing {
						text := content.String()
						if strings.Contains(text, "<think>") && !strings.Contains(text, "</think>") {
							progress = "Analyzing research evidence"
						} else if strings.TrimSpace(removeTags(text)) != "" {
							writing = true
						}
					}
					if writing {
						progress = "Writing research report"
					}
				}
				if choice.FinishReason != "" {
					if choice.FinishReason != "stop" {
						yield(nil, fmt.Errorf("perplexity research incomplete: %s", choice.FinishReason))
						return
					}
					finished = true
				}
			}
			if progress != "" && progress != lastProgress {
				lastProgress = progress
				if !yield(&researcher.Result{Progress: progress}, nil) {
					return
				}
			}
		}
		if err := stream.Err(); err != nil {
			yield(nil, err)
			return
		}
		if !finished {
			yield(nil, errors.New("perplexity research: stream ended before completion"))
			return
		}
		report := strings.TrimSpace(removeTags(content.String()))
		if len(citations) == 0 {
			citations = sourceOrder
		}
		if len(citations) > 0 {
			var sources strings.Builder
			sources.WriteString("\n\nSources:")
			for i, url := range citations {
				if title := titles[url]; title != "" {
					fmt.Fprintf(&sources, "\n[%d] %s: %s", i+1, title, url)
				} else {
					fmt.Fprintf(&sources, "\n[%d] %s", i+1, url)
				}
			}
			report += sources.String()
		}
		yield(&researcher.Result{Content: report}, nil)
	}
}

var thinkingTags = regexp.MustCompile(`(?s)<think>.*?</think>`)

func removeTags(text string) string {
	text = thinkingTags.ReplaceAllString(text, "")
	if i := strings.Index(text, "<think>"); i >= 0 {
		text = text[:i]
	}
	return text
}
