package classifier

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

const (
	maxQueryChars = 2000

	// embedTimeout bounds Tier 2 on the routing hot path. Routing must never
	// stall a request on a slow embedding backend.
	embedTimeout = 2 * time.Second

	// warmupTimeout bounds the background centroid initialization started at
	// construction time.
	warmupTimeout = 30 * time.Second
)

// centroidCache embeds each candidate's example utterances and keeps the
// per-candidate mean vector ("centroid") in memory. A failed initialization is
// retried on a later request instead of being latched permanently. A candidate
// with no examples leaves a nil centroid and simply doesn't participate in
// Tier 2.
type centroidCache struct {
	candidates []Candidate
	embedder   provider.Embedder

	// sem is a one-permit semaphore serializing initialization; unlike a
	// mutex, waiting on it respects context cancellation.
	sem chan struct{}

	vectors atomic.Pointer[[][]float32]
}

func newCentroidCache(candidates []Candidate, embedder provider.Embedder) *centroidCache {
	return &centroidCache{
		candidates: candidates,
		embedder:   embedder,

		sem: make(chan struct{}, 1),
	}
}

// get returns the centroids, initializing them on first use. Waiting for an
// initialization already running elsewhere is bounded by ctx; on failure it
// returns nil and the next call retries.
func (cc *centroidCache) get(ctx context.Context) [][]float32 {
	if v := cc.vectors.Load(); v != nil {
		return *v
	}

	select {
	case cc.sem <- struct{}{}:
	case <-ctx.Done():
		return nil
	}

	defer func() { <-cc.sem }()

	if v := cc.vectors.Load(); v != nil {
		return *v
	}

	vectors, ok := cc.build(ctx)

	if !ok {
		return nil
	}

	cc.vectors.Store(&vectors)

	return vectors
}

func (cc *centroidCache) build(ctx context.Context) ([][]float32, bool) {
	vectors := make([][]float32, len(cc.candidates))

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
		return vectors, true
	}

	result, err := cc.embedder.Embed(ctx, texts, nil)

	if err != nil || result == nil || len(result.Embeddings) != len(texts) {
		return nil, false
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

		vectors[i] = centroid
	}

	return vectors, true
}

// embedPick scores the task against each eligible candidate's centroid. The
// pick resolves only when the best candidate beats the runner-up by the
// configured margin — a scale-invariant test, since absolute cosine values
// vary widely between embedding models. It never errors: any failure returns
// (-1, false) and the caller keeps the heuristic pick.
func (c *Completer) embedPick(ctx context.Context, s signals, eligible []int) (int, bool) {
	if c.centroids == nil || s.queryText == "" {
		return -1, false
	}

	ctx, cancel := context.WithTimeout(ctx, embedTimeout)
	defer cancel()

	vectors := c.centroids.get(ctx)

	if vectors == nil {
		return -1, false
	}

	query := truncateText(s.queryText, maxQueryChars)

	result, err := c.embedder.Embed(ctx, []string{query}, nil)

	if err != nil || result == nil || len(result.Embeddings) == 0 {
		return -1, false
	}

	queryVec := result.Embeddings[0]

	best := -1
	scored := 0

	var bestScore, secondScore float32 = -1, -1

	for _, i := range eligible {
		centroid := vectors[i]

		if centroid == nil {
			continue
		}

		scored++

		score := provider.CosineSimilarity(queryVec, centroid)

		if score > bestScore {
			secondScore = bestScore
			bestScore = score
			best = i
		} else if score > secondScore {
			secondScore = score
		}
	}

	// A margin needs a runner-up: with fewer than two scored candidates the
	// similarity carries no comparative signal.
	if best == -1 || scored < 2 {
		return -1, false
	}

	return best, float64(bestScore-secondScore) >= c.margin
}
