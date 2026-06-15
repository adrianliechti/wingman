package reranker

import (
	"context"
	"errors"
	"sort"

	"github.com/adrianliechti/wingman/pkg/provider"
)

var _ provider.Reranker = (*EmbedderAdapter)(nil)

type EmbedderAdapter struct {
	model string

	embedder provider.Embedder
}

func FromEmbedder(model string, embedder provider.Embedder) *EmbedderAdapter {
	return &EmbedderAdapter{
		model: model,

		embedder: embedder,
	}
}

func (a *EmbedderAdapter) Rerank(ctx context.Context, query string, texts []string, options *provider.RerankOptions) ([]provider.Ranking, error) {
	if options == nil {
		options = new(provider.RerankOptions)
	}

	queryResult, err := a.embedder.Embed(ctx, []string{query}, nil)

	if err != nil {
		return nil, err
	}

	if len(queryResult.Embeddings) == 0 {
		return nil, errors.New("no embedding returned for query")
	}

	textsResult, err := a.embedder.Embed(ctx, texts, nil)

	if err != nil {
		return nil, err
	}

	if len(textsResult.Embeddings) != len(texts) {
		return nil, errors.New("embedding count does not match text count")
	}

	var results []provider.Ranking

	for i, text := range texts {
		score := provider.CosineSimilarity(queryResult.Embeddings[0], textsResult.Embeddings[i])

		results = append(results, provider.Ranking{
			Text:  text,
			Score: float64(score),
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
