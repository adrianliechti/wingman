package classifier

import (
	"context"
	"strings"
	"sync"

	"github.com/adrianliechti/wingman/pkg/provider"
)

const maxQueryChars = 2000

// centroidCache lazily embeds each candidate's example utterances once and
// keeps the per-candidate mean vector ("centroid") in memory. A candidate with
// no examples — or any embedding failure — leaves a nil centroid and simply
// doesn't participate in Tier 2.
type centroidCache struct {
	candidates []Candidate
	embedder   provider.Embedder

	once    sync.Once
	vectors [][]float32
}

func newCentroidCache(candidates []Candidate, embedder provider.Embedder) *centroidCache {
	return &centroidCache{
		candidates: candidates,
		embedder:   embedder,
	}
}

func (cc *centroidCache) ensure(ctx context.Context) {
	cc.once.Do(func() {
		cc.vectors = make([][]float32, len(cc.candidates))

		var texts []string
		var owner []int

		for i, cand := range cc.candidates {
			for _, ex := range cand.Examples {
				ex = strings.TrimSpace(ex)

				if ex == "" {
					continue
				}

				texts = append(texts, ex)
				owner = append(owner, i)
			}
		}

		if len(texts) == 0 {
			return
		}

		result, err := cc.embedder.Embed(ctx, texts, nil)

		if err != nil || result == nil || len(result.Embeddings) != len(texts) {
			return
		}

		sums := make([][]float64, len(cc.candidates))
		counts := make([]int, len(cc.candidates))

		for k, vec := range result.Embeddings {
			i := owner[k]

			if sums[i] == nil {
				sums[i] = make([]float64, len(vec))
			}

			if len(vec) != len(sums[i]) {
				continue
			}

			for d := range vec {
				sums[i][d] += float64(vec[d])
			}

			counts[i]++
		}

		for i := range cc.candidates {
			if counts[i] == 0 {
				continue
			}

			centroid := make([]float32, len(sums[i]))

			for d := range sums[i] {
				centroid[d] = float32(sums[i][d] / float64(counts[i]))
			}

			cc.vectors[i] = centroid
		}
	})
}

// embedPick scores the task against each eligible candidate's centroid and
// returns the best match plus whether it cleared the similarity threshold. It
// never errors: any failure returns (-1, false) and the caller degrades to the
// heuristic pick.
func (c *Completer) embedPick(ctx context.Context, s signals, eligible []int) (int, bool) {
	if c.centroids == nil {
		return -1, false
	}

	c.centroids.ensure(ctx)

	query := s.queryText

	if query == "" {
		return -1, false
	}

	if len(query) > maxQueryChars {
		query = query[:maxQueryChars]
	}

	result, err := c.embedder.Embed(ctx, []string{query}, nil)

	if err != nil || result == nil || len(result.Embeddings) == 0 {
		return -1, false
	}

	queryVec := result.Embeddings[0]

	best := -1
	var bestScore float32 = -1

	for _, i := range eligible {
		centroid := c.centroids.vectors[i]

		if centroid == nil {
			continue
		}

		score := provider.CosineSimilarity(queryVec, centroid)

		if score > bestScore {
			bestScore = score
			best = i
		}
	}

	if best == -1 {
		return -1, false
	}

	return best, float64(bestScore) >= c.threshold
}
