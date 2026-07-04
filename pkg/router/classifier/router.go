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
// high-volume, single-shot traffic. Every request carries an eligible fallback
// candidate, so a single bad backend can never break it.
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

	// Margin is the minimum cosine-similarity advantage the best candidate
	// must hold over the runner-up for the embedding tier to resolve a pick
	// (default 0.05). A relative margin transfers across embedding models,
	// unlike an absolute similarity threshold. Only used when Embedder is set.
	Margin float64

	// Judge enables Tier 3 (LLM-as-judge). Nil disables it.
	Judge provider.Completer

	// DefaultIndex is the universal fail-safe candidate.
	DefaultIndex int
}

const (
	defaultMargin     = 0.05
	decisionCacheSize = 1024

	// ambiguityMargin is the assumed error of the difficulty estimate. The
	// heuristic is uncertain only when shifting the score by this much would
	// change the picked candidate; a score near a boundary that both sides
	// resolve to the same model needs no escalation.
	ambiguityMargin = 0.4
)

// decision is a routing outcome: the picked candidate and the eligible
// fallback to stream from when the pick fails before producing output.
type decision struct {
	index    int
	fallback int
}

type Completer struct {
	candidates []Candidate

	defaultIndex int

	embedder provider.Embedder
	margin   float64

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

	margin := opts.Margin
	if margin <= 0 {
		margin = defaultMargin
	}

	c := &Completer{
		candidates: candidates,

		defaultIndex: def,

		embedder: opts.Embedder,
		margin:   margin,

		judge: opts.Judge,

		decisionCache: newLRU(decisionCacheSize),
	}

	if opts.Embedder != nil {
		c.centroids = newCentroidCache(candidates, opts.Embedder)

		// Pre-warm the centroids off the request path, so the first ambiguous
		// request doesn't pay the example-embedding latency.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), warmupTimeout)
			defer cancel()

			c.centroids.get(ctx)
		}()
	}

	return c, nil
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	d := c.classify(ctx, messages, options)

	return func(yield func(*provider.Completion, error) bool) {
		emitted := false

		for completion, err := range c.candidates[d.index].Completer.Complete(ctx, messages, options) {
			// A hard failure before any output is produced falls back once, so
			// a single bad backend can't break the request. Once output has
			// streamed, errors propagate normally.
			if err != nil && !emitted && d.fallback != d.index {
				for completion, err := range c.candidates[d.fallback].Completer.Complete(ctx, messages, options) {
					if !yield(completion, err) {
						return
					}
				}

				return
			}

			// Only meaningful output counts as emitted: providers yield a
			// role-only delta as the first stream chunk, and a stream that
			// dies right after it should still fall back.
			if completion != nil && completion.Message != nil && len(completion.Message.Content) > 0 {
				emitted = true
			}

			if !yield(completion, err) {
				return
			}
		}

		// A stream that completed without any content is an empty answer —
		// treat it like a failure and retry on the fallback.
		if !emitted && d.fallback != d.index {
			for completion, err := range c.candidates[d.fallback].Completer.Complete(ctx, messages, options) {
				if !yield(completion, err) {
					return
				}
			}
		}
	}
}

// classify resolves the routing decision for a request, caching it so a task's
// own tool round-trips don't re-run the cascade.
func (c *Completer) classify(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) decision {
	s := extractSignals(messages, options)
	fp := fingerprint(s)

	// A cached decision must still satisfy the hard constraints: the
	// fingerprint is keyed on the user instruction, but tool round-trips grow
	// the context and can push it past a cached candidate's MaxContext.
	if d, ok := c.decisionCache.get(fp); ok && isEligible(c.candidates[d.index], s) && isEligible(c.candidates[d.fallback], s) {
		return d
	}

	d := c.decide(ctx, s)
	c.decisionCache.put(fp, d)

	return d
}

func (c *Completer) decide(ctx context.Context, s signals) decision {
	// Tier 1: hard-constraint prefilter.
	eligible := make([]int, 0, len(c.candidates))

	for i := range c.candidates {
		if isEligible(c.candidates[i], s) {
			eligible = append(eligible, i)
		}
	}

	if len(eligible) == 0 {
		return decision{c.defaultIndex, c.defaultIndex}
	}

	if len(eligible) == 1 {
		return decision{eligible[0], eligible[0]}
	}

	// Tier 1: difficulty estimate + cheapest-good-enough pick.
	score := difficultyScore(s)

	pick := c.cheapestClearing(eligible, roundLevel(score))

	// Pick stability decides confidence: escalation buys nothing when the
	// score, shifted by the estimate's assumed error in either direction,
	// still resolves to the same candidate.
	confident := c.cheapestClearing(eligible, roundLevel(score-ambiguityMargin)) == pick &&
		c.cheapestClearing(eligible, roundLevel(score+ambiguityMargin)) == pick

	if confident || (c.embedder == nil && c.judge == nil) {
		return c.resolve(eligible, pick)
	}

	// Tier 2: embedding similarity. Only a resolved pick (best clears the
	// runner-up by the margin) may override the heuristic — an indecisive
	// argmax is noise, not signal.
	if c.embedder != nil {
		if best, resolved := c.embedPick(ctx, s, eligible); resolved {
			return c.resolve(eligible, best)
		}
	}

	// Tier 3: LLM-as-judge (optional). The decision is cached by classify, so a
	// task's tool round-trips don't re-issue this call.
	if c.judge != nil {
		if k := c.judgePick(ctx, s, eligible); k >= 0 {
			return c.resolve(eligible, k)
		}
	}

	return c.resolve(eligible, pick)
}

// resolve pairs a pick with its fallback: the default candidate when it is
// eligible and not already the pick, otherwise the most capable other eligible
// candidate (ties broken by cost). With no alternative the pick backs itself.
func (c *Completer) resolve(eligible []int, index int) decision {
	if index != c.defaultIndex {
		for _, i := range eligible {
			if i == c.defaultIndex {
				return decision{index, c.defaultIndex}
			}
		}
	}

	fallback := index

	for _, i := range eligible {
		if i == index {
			continue
		}

		if fallback == index {
			fallback = i
			continue
		}

		ci := c.candidates[i]
		cf := c.candidates[fallback]

		if ci.MaxDifficulty > cf.MaxDifficulty || (ci.MaxDifficulty == cf.MaxDifficulty && ci.Cost < cf.Cost) {
			fallback = i
		}
	}

	return decision{index, fallback}
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
