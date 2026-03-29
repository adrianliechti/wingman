package reranker

import (
	"context"
	"math"
	"sort"

	"github.com/adrianliechti/wingman/pkg/provider"
)

var _ provider.Reranker = (*Adapter)(nil)

type Adapter struct {
	model string

	embedder provider.Embedder
}

func FromEmbedder(model string, embedder provider.Embedder) *Adapter {
	return &Adapter{
		model: model,

		embedder: embedder,
	}
}

func (a *Adapter) Rerank(ctx context.Context, query string, texts []string, options *provider.RerankOptions) ([]provider.Ranking, error) {
	if options == nil {
		options = new(provider.RerankOptions)
	}

	queryResult, err := a.embedder.Embed(ctx, []string{query}, nil)

	if err != nil {
		return nil, err
	}

	textsResult, err := a.embedder.Embed(ctx, texts, nil)

	if err != nil {
		return nil, err
	}

	var results []provider.Ranking

	for i, text := range texts {
		score := cosineSimilarity(queryResult.Embeddings[0], textsResult.Embeddings[i])

		results = append(results, provider.Ranking{
			Text:  text,
			Score: float64(score),
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if options.Limit != nil {
		limit := min(*options.Limit, len(results))

		results = results[:limit]
	}

	return results, nil
}

func cosineSimilarity(a []float32, b []float32) float32 {
	if len(a) != len(b) {
		return 0.0
	}

	dotproduct := 0.0

	magnitudeA := 0.0
	magnitudeB := 0.0

	for k := range a {
		valA := float64(a[k])
		valB := float64(b[k])

		dotproduct += valA * valB

		magnitudeA += math.Pow(valA, 2)
		magnitudeB += math.Pow(valB, 2)
	}

	if magnitudeA == 0 || magnitudeB == 0 {
		return 0.0
	}

	return float32(dotproduct / (math.Sqrt(magnitudeA) * math.Sqrt(magnitudeB)))
}
