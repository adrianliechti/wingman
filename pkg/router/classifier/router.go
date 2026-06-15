// Package classifier implements a per-task model router. Unlike the
// roundrobin/adaptive routers (which load-balance by provider health), it picks
// the best candidate model for each request via a cheap-to-expensive cascade:
//
//  1. local heuristics + hard-constraint prefilter (every request, no network),
//  2. embedding similarity vs per-candidate exemplars (only when ambiguous),
//  3. an LLM-as-judge (optional, off by default, only the residual cases).
//
// With no embedder and no judge configured it is a pure local heuristic router,
// adding zero network latency to the hot path — the intended default for
// high-volume, single-shot traffic. Every step fails safe to a default
// candidate and can never break a request.
package classifier

import (
	"context"
	"errors"
	"iter"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// Candidate is a routable backend plus the metadata the cascade scores it on.
// Cost is only ever used to break ties among candidates that already clear the
// difficulty bar; it is never weighed against capability, so the router can't
// be biased toward expensive models.
type Candidate struct {
	Completer provider.Completer

	Model string
	Card  string

	Cost          float64
	MaxDifficulty int
	Vision        bool
	MaxContext    int

	Examples []string
}

// Options configures the optional cascade tiers. Each tier is disabled when its
// dependency is nil.
type Options struct {
	// Embedder enables Tier 2 (embedding similarity). Nil disables it.
	Embedder provider.Embedder

	// Threshold is the minimum cosine similarity for the embedding tier to
	// resolve a pick (default 0.75). Only used when Embedder is set.
	Threshold float64

	// Judge enables Tier 3 (LLM-as-judge). Nil disables it.
	Judge provider.Completer

	// DefaultIndex is the universal fail-safe candidate.
	DefaultIndex int
}

const (
	defaultThreshold  = 0.75
	decisionCacheSize = 1024

	// ambiguityMargin is how close (in difficulty units) the estimated task
	// difficulty must be to a candidate's MaxDifficulty boundary before the
	// heuristic is considered uncertain and the request escalates.
	ambiguityMargin = 0.4
)

type Completer struct {
	candidates []Candidate

	defaultIndex int

	embedder  provider.Embedder
	threshold float64

	judge provider.Completer

	decisionCache *lruCache

	centroids *centroidCache
}

var _ provider.Completer = (*Completer)(nil)

func NewCompleter(candidates []Candidate, opts Options) (*Completer, error) {
	if len(candidates) == 0 {
		return nil, errors.New("classifier router requires at least one candidate")
	}

	def := opts.DefaultIndex
	if def < 0 || def >= len(candidates) {
		def = 0
	}

	thresh := opts.Threshold
	if thresh <= 0 {
		thresh = defaultThreshold
	}

	c := &Completer{
		candidates: candidates,

		defaultIndex: def,

		embedder:  opts.Embedder,
		threshold: thresh,

		judge: opts.Judge,

		decisionCache: newLRU(decisionCacheSize),
	}

	if opts.Embedder != nil {
		c.centroids = newCentroidCache(candidates, opts.Embedder)
	}

	return c, nil
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	index := c.classify(ctx, messages, options)

	return func(yield func(*provider.Completion, error) bool) {
		emitted := false

		for completion, err := range c.candidates[index].Completer.Complete(ctx, messages, options) {
			// A hard failure before any output is produced falls back once to
			// the default candidate, so a single bad backend can't break the
			// request. Once output has streamed, errors propagate normally.
			if err != nil && !emitted && index != c.defaultIndex {
				for completion, err := range c.candidates[c.defaultIndex].Completer.Complete(ctx, messages, options) {
					if !yield(completion, err) {
						return
					}
				}

				return
			}

			if completion != nil {
				emitted = true
			}

			if !yield(completion, err) {
				return
			}
		}
	}
}

// classify resolves the chosen candidate index for a request, caching the
// decision so a task's own tool round-trips don't re-run the cascade.
func (c *Completer) classify(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) int {
	s := extractSignals(messages, options)
	fp := fingerprint(s)

	if index, ok := c.decisionCache.get(fp); ok {
		return index
	}

	index := c.decide(ctx, s)
	c.decisionCache.put(fp, index)

	return index
}

func (c *Completer) decide(ctx context.Context, s signals) int {
	// Tier 1: hard-constraint prefilter.
	eligible := make([]int, 0, len(c.candidates))

	for i := range c.candidates {
		if isEligible(c.candidates[i], s) {
			eligible = append(eligible, i)
		}
	}

	if len(eligible) == 0 {
		return c.defaultIndex
	}

	if len(eligible) == 1 {
		return eligible[0]
	}

	// Tier 1: difficulty estimate + cheapest-good-enough pick.
	score := difficultyScore(s)
	level := roundLevel(score)

	pick := c.cheapestClearing(eligible, level)

	confident := true

	for _, i := range eligible {
		boundary := float64(c.candidates[i].MaxDifficulty) + 0.5

		if abs(score-boundary) < ambiguityMargin {
			confident = false
			break
		}
	}

	if confident || (c.embedder == nil && c.judge == nil) {
		return pick
	}

	// Tier 2: embedding similarity.
	if c.embedder != nil {
		if best, resolved := c.embedPick(ctx, s, eligible); best >= 0 {
			if resolved || c.judge == nil {
				return best
			}

			pick = best
		}
	}

	// Tier 3: LLM-as-judge (optional). The decision is cached by classify, so a
	// task's tool round-trips don't re-issue this call.
	if c.judge != nil {
		if k := c.judgePick(ctx, s, eligible); k >= 0 && containsInt(eligible, k) {
			return k
		}
	}

	return pick
}

// cheapestClearing returns the cheapest eligible candidate whose MaxDifficulty
// clears the estimated level. If none clear it, it returns the most capable
// eligible candidate (breaking ties by cost).
func (c *Completer) cheapestClearing(eligible []int, level int) int {
	best := -1

	for _, i := range eligible {
		if c.candidates[i].MaxDifficulty < level {
			continue
		}

		if best == -1 || c.candidates[i].Cost < c.candidates[best].Cost {
			best = i
		}
	}

	if best != -1 {
		return best
	}

	best = eligible[0]

	for _, i := range eligible[1:] {
		ci := c.candidates[i]
		cb := c.candidates[best]

		if ci.MaxDifficulty > cb.MaxDifficulty || (ci.MaxDifficulty == cb.MaxDifficulty && ci.Cost < cb.Cost) {
			best = i
		}
	}

	return best
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}

	return false
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}

	return x
}
