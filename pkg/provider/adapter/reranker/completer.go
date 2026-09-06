package reranker

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

var _ provider.Reranker = (*CompleterAdapter)(nil)

type CompleterAdapter struct {
	model string

	completer provider.Completer
}

func FromCompleter(model string, completer provider.Completer) *CompleterAdapter {
	return &CompleterAdapter{
		model: model,

		completer: completer,
	}
}

var rankingsSchema = &provider.Schema{
	Name:        "rankings",
	Description: "Relevance scores for the provided documents",

	Properties: map[string]any{
		"type": "object",

		"properties": map[string]any{
			"rankings": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"index": map[string]any{
							"type":        "integer",
							"description": "the index of the document",
						},
						"score": map[string]any{
							"type":        "number",
							"description": "the relevance score between 0.0 (irrelevant) and 1.0 (highly relevant)",
						},
					},
					"required":             []string{"index", "score"},
					"additionalProperties": false,
				},
			},
		},

		"required":             []string{"rankings"},
		"additionalProperties": false,
	},
}

func (a *CompleterAdapter) Rerank(ctx context.Context, query string, texts []string, options *provider.RerankOptions) ([]provider.Ranking, error) {
	if options == nil {
		options = new(provider.RerankOptions)
	}

	var prompt strings.Builder

	prompt.WriteString("Query:\n")
	prompt.WriteString(query)
	prompt.WriteString("\n\nDocuments:\n")

	for i, text := range texts {
		prompt.WriteString("[")
		prompt.WriteString(strconv.Itoa(i))
		prompt.WriteString("] ")
		prompt.WriteString(text)
		prompt.WriteString("\n")
	}

	messages := []provider.Message{
		provider.SystemMessage("Act as a reranker. Score the relevance of each document to the query. Return a score for every document index exactly once."),
		provider.UserMessage(prompt.String()),
	}

	completeOptions := &provider.CompleteOptions{
		Schema: rankingsSchema,
	}

	acc := provider.CompletionAccumulator{}

	for completion, err := range a.completer.Complete(ctx, messages, completeOptions) {
		if err != nil {
			return nil, err
		}

		acc.Add(*completion)
	}

	result := acc.Result()

	var data struct {
		Rankings []struct {
			Index int     `json:"index"`
			Score float64 `json:"score"`
		} `json:"rankings"`
	}

	if err := json.Unmarshal([]byte(result.Text()), &data); err != nil {
		return nil, err
	}

	seen := make(map[int]bool)

	var results []provider.Ranking

	for _, r := range data.Rankings {
		if r.Index < 0 || r.Index >= len(texts) || seen[r.Index] {
			continue
		}

		seen[r.Index] = true

		results = append(results, provider.Ranking{
			Text:  texts[r.Index],
			Score: r.Score,
		})
	}

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if options.Limit != nil {
		limit := min(*options.Limit, len(results))

		results = results[:limit]
	}

	return results, nil
}
