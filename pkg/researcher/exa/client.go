package exa

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"iter"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/adrianliechti/wingman/pkg/researcher"

	"github.com/openai/openai-go/v3/packages/ssestream"
)

var _ researcher.Provider = &Client{}

type Client struct {
	url    string
	token  string
	client *http.Client
}

func New(token string, options ...Option) (*Client, error) {
	c := &Client{
		url: "https://api.exa.ai",

		token:  token,
		client: http.DefaultClient,
	}

	for _, option := range options {
		option(c)
	}

	if c.token == "" {
		return nil, errors.New("invalid token")
	}

	return c, nil
}

func (c *Client) Research(ctx context.Context, instructions string, options *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
	return func(yield func(*researcher.Result, error) bool) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		if !yield(&researcher.Result{Progress: "Searching and synthesizing web evidence"}, nil) {
			return
		}
		body, err := json.Marshal(&SearchRequest{Query: instructions, Type: "deep-reasoning", Stream: true,
			SystemPrompt: "Honor the research task's requested timeframes, document versions, and source restrictions. Use the supplied source URLs when requested. Cite the source URL for each supported claim; do not use unrelated sources or invent citation references. If requested sources cannot be verified, explicitly say so. Follow the requested report length and format. Research task: " + instructions,
			OutputSchema: &OutputSchema{Type: "text", Description: "A source-backed research report following the research task's instructions, length, and format, with cited source URLs."},
		})
		if err != nil {
			yield(nil, err)
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.url, "/")+"/search", bytes.NewReader(body))
		if err != nil {
			yield(nil, err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("x-api-key", c.token)
		resp, err := c.client.Do(req)
		if err != nil {
			yield(nil, err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			yield(nil, fmt.Errorf("exa: unexpected status %d: %s", resp.StatusCode, string(body)))
			return
		}
		// The done event and a non-streaming response carry the same output.
		finish := func(output *SearchOutput) {
			if output == nil {
				yield(nil, errors.New("exa: no research output returned"))
				return
			}
			sources := collectSources(output.Grounding)
			progress := "Received research report"
			if len(sources) > 0 {
				progress = fmt.Sprintf("Received research report with %d cited sources", len(sources))
			}
			if !yield(&researcher.Result{Progress: progress}, nil) {
				return
			}
			yield(&researcher.Result{Content: appendSources(strings.TrimSpace(decodeContent(output.Content)), sources)}, nil)
		}
		mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if mediaType != "text/event-stream" {
			// Accept a normal response from deployments that do not stream search.
			var data SearchResponse
			if err := json.UnmarshalRead(resp.Body, &data); err != nil {
				yield(nil, err)
				return
			}
			finish(data.Output)
			return
		}
		stream := ssestream.NewStream[searchEvent](ssestream.NewDecoder(resp), nil)
		defer stream.Close()
		// Text deltas only report drafting progress: Exa can discard a draft with
		// stream-reset, and the done event carries the final report and grounding.
		chars, lastChars := 0, -1
		for stream.Next() {
			event := stream.Current()
			switch event.Type {
			case "stream-reset":
				chars, lastChars = 0, -1
			case "grounding":
				if sources := len(collectSources(event.Grounding)); sources > 0 {
					if !yield(&researcher.Result{Progress: fmt.Sprintf("Found %d research sources", sources)}, nil) {
						return
					}
				}
			case "text-delta":
				chars += utf8.RuneCountInString(event.Delta)
				if chars > 0 && (lastChars < 0 || chars-lastChars >= 1000) {
					progress := "Writing research report"
					if lastChars >= 0 {
						progress = fmt.Sprintf("Writing research report (%d characters)", chars)
					}
					lastChars = chars
					if !yield(&researcher.Result{Progress: progress}, nil) {
						return
					}
				}
			case "done":
				finish(event.Output)
				return
			}
		}
		if err := stream.Err(); err != nil {
			yield(nil, err)
			return
		}
		yield(nil, errors.New("exa research: stream ended before completion"))
	}
}

type searchEvent struct {
	Type      string        `json:"type"`
	Delta     string        `json:"delta"`
	Grounding []Grounding   `json:"grounding"`
	Output    *SearchOutput `json:"output"`
}

type source struct {
	title string
	url   string
}

// decodeContent returns the synthesized text. With a "text" outputSchema the
// content is a JSON string; fall back to the raw bytes for any other shape.
func decodeContent(raw jsontext.Value) string {
	if len(raw) == 0 {
		return ""
	}

	var text string

	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}

	return string(raw)
}

func collectSources(grounding []Grounding) []source {
	var sources []source
	seen := map[string]struct{}{}

	for _, g := range grounding {
		for _, c := range g.Citations {
			if c.URL == "" {
				continue
			}

			if _, ok := seen[c.URL]; ok {
				continue
			}

			seen[c.URL] = struct{}{}
			sources = append(sources, source{title: c.Title, url: c.URL})
		}
	}

	return sources
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
