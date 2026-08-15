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
	"math"
	"slices"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/request"
	"github.com/adrianliechti/wingman/pkg/router/internal/executor"
	"github.com/adrianliechti/wingman/pkg/router/internal/routingtelemetry"
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
	// Name identifies this router in telemetry. It defaults to "classifier".
	Name string

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

	// SessionAffinity reuses one decision for matching requests in the same
	// session/task. It requires normalized request metadata in ctx.
	SessionAffinity bool

	// MessageHashFallback uses the latest user instruction as an affinity key
	// when a request has no session metadata. It is opt-in because identical
	// instructions from unrelated callers otherwise share decisions.
	MessageHashFallback bool

	// FirstTokenTimeout bounds the wait for meaningful content from each
	// candidate. Zero disables the deadline.
	FirstTokenTimeout time.Duration
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

// decision is an ordered routing outcome. The classifier pick comes first,
// followed by every other eligible fallback in preference order.
type decision struct {
	candidates []int
	source     string
	score      float64
	cached     bool
}

type Completer struct {
	candidates []Candidate
	name       string

	defaultIndex int

	embedder provider.Embedder
	margin   float64

	judge provider.Completer

	sessionAffinity     bool
	messageHashFallback bool
	firstTokenTimeout   time.Duration

	decisionCache *lruCache

	centroids *centroidCache
}

var _ provider.Completer = (*Completer)(nil)

func NewCompleter(candidates []Candidate, opts Options) (*Completer, error) {
	if len(candidates) == 0 {
		return nil, errors.New("classifier router requires at least one candidate")
	}

	seen := make(map[string]struct{}, len(candidates))

	for _, candidate := range candidates {
		switch {
		case candidate.Completer == nil:
			return nil, errors.New("classifier candidate requires a completer")
		case candidate.Model == "":
			return nil, errors.New("classifier candidate requires a model")
		case math.IsNaN(candidate.Cost) || math.IsInf(candidate.Cost, 0) || candidate.Cost < 0:
			return nil, errors.New("classifier candidate cost must be finite and non-negative: " + candidate.Model)
		case candidate.MaxDifficulty < 0 || candidate.MaxDifficulty > maxLevel:
			return nil, errors.New("classifier candidate max difficulty must be between 0 and 4: " + candidate.Model)
		case candidate.MaxContext < 0:
			return nil, errors.New("classifier candidate max context must not be negative: " + candidate.Model)
		}

		if _, ok := seen[candidate.Model]; ok {
			return nil, errors.New("classifier candidate models must be unique: " + candidate.Model)
		}

		seen[candidate.Model] = struct{}{}
	}

	def := opts.DefaultIndex
	if def < 0 || def >= len(candidates) {
		return nil, errors.New("classifier default index is out of range")
	}

	margin := opts.Margin
	if margin == 0 {
		margin = defaultMargin
	}
	if math.IsNaN(margin) || math.IsInf(margin, 0) || margin < 0 || margin > 2 {
		return nil, errors.New("classifier margin must be finite and between 0 and 2")
	}
	if opts.FirstTokenTimeout < 0 {
		return nil, errors.New("classifier first token timeout must not be negative")
	}

	c := &Completer{
		candidates: candidates,
		name:       opts.Name,

		defaultIndex: def,

		embedder: opts.Embedder,
		margin:   margin,

		judge: opts.Judge,

		sessionAffinity:     opts.SessionAffinity,
		messageHashFallback: opts.MessageHashFallback,
		firstTokenTimeout:   opts.FirstTokenTimeout,

		decisionCache: newLRU(decisionCacheSize),
	}
	if c.name == "" {
		c.name = "classifier"
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
	candidates := make([]executor.Candidate, 0, len(d.candidates))

	for _, index := range d.candidates {
		candidate := c.candidates[index]
		candidates = append(candidates, executor.Candidate{ID: candidate.Model, Completer: candidate.Completer})
	}

	routingtelemetry.RecordDecision(ctx, routingtelemetry.Decision{
		Router:     c.name,
		Kind:       "classifier",
		Model:      candidates[0].ID,
		Source:     d.source,
		Cached:     d.cached,
		Score:      d.score,
		Candidates: len(candidates),
	})

	hooks := executor.Hooks{
		Attempt: func(candidate executor.Candidate, result executor.Result) {
			routingtelemetry.RecordAttempt(ctx, routingtelemetry.Attempt{
				Router:    c.name,
				Kind:      "classifier",
				Model:     candidate.ID,
				Failure:   string(result.Failure),
				Delivered: result.Delivered,
				TTFT:      result.TTFT,
			})
		},
		Fallback: func(from, to executor.Candidate, result executor.Result) {
			routingtelemetry.RecordFallback(ctx, routingtelemetry.Fallback{
				Router: c.name,
				Kind:   "classifier",
				From:   from.ID,
				To:     to.ID,
				Reason: string(result.Failure),
			})
		},
	}

	return executor.Complete(ctx, candidates, messages, options, c.firstTokenTimeout, hooks)
}

// classify resolves the routing decision for a request, caching it so a task's
// own tool round-trips don't re-run the cascade.
func (c *Completer) classify(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) decision {
	s := extractSignals(messages, options)
	identity := c.affinityIdentity(ctx, s)
	fp := fingerprint(s, identity)

	// A cached decision must still satisfy the hard constraints: the
	// fingerprint is keyed on the user instruction, but tool round-trips grow
	// the context and can push it past a cached candidate's MaxContext.
	if d, ok := c.decisionCache.get(fp); identity != "" && ok {
		eligible := true
		for _, index := range d.candidates {
			if !isEligible(c.candidates[index], s) {
				eligible = false
				break
			}
		}

		if eligible {
			d.cached = true
			return d
		}
	}

	d := c.decide(ctx, s)
	if identity != "" {
		c.decisionCache.put(fp, d)
	}

	return d
}

func (c *Completer) affinityIdentity(ctx context.Context, s signals) string {
	if c.sessionAffinity {
		if metadata, ok := request.FromContext(ctx); ok {
			identity := metadata.RoutingIdentity()
			if identity != "" {
				if metadata.TaskID != "" {
					identity += ":task:" + metadata.TaskID
				}
				return identity
			}
		}
	}

	if c.messageHashFallback && s.queryText != "" {
		return "message:" + s.queryText
	}

	return ""
}

func (c *Completer) decide(ctx context.Context, s signals) decision {
	score := difficultyScore(s)

	// Tier 1: hard-constraint prefilter.
	eligible := make([]int, 0, len(c.candidates))

	for i := range c.candidates {
		if isEligible(c.candidates[i], s) {
			eligible = append(eligible, i)
		}
	}

	if len(eligible) == 0 {
		return decision{candidates: []int{c.defaultIndex}, source: "default", score: score}
	}

	if len(eligible) == 1 {
		return decision{candidates: []int{eligible[0]}, source: "constraint", score: score}
	}

	// Tier 1: difficulty estimate + cheapest-good-enough pick.
	pick := c.cheapestClearing(eligible, roundLevel(score))

	// Pick stability decides confidence: escalation buys nothing when the
	// score, shifted by the estimate's assumed error in either direction,
	// still resolves to the same candidate.
	confident := c.cheapestClearing(eligible, roundLevel(score-ambiguityMargin)) == pick &&
		c.cheapestClearing(eligible, roundLevel(score+ambiguityMargin)) == pick

	if confident || (c.embedder == nil && c.judge == nil) {
		return c.resolve(eligible, pick, "heuristic", score)
	}

	// Tier 2: embedding similarity. Only a resolved pick (best clears the
	// runner-up by the margin) may override the heuristic — an indecisive
	// argmax is noise, not signal.
	if c.embedder != nil {
		if best, resolved := c.embedPick(ctx, s, eligible); resolved {
			return c.resolve(eligible, best, "embedding", score)
		}
	}

	// Tier 3: LLM-as-judge (optional). The decision is cached by classify, so a
	// task's tool round-trips don't re-issue this call.
	if c.judge != nil {
		if k := c.judgePick(ctx, s, eligible); k >= 0 {
			return c.resolve(eligible, k, "judge", score)
		}
	}

	return c.resolve(eligible, pick, "heuristic", score)
}

// resolve orders all eligible candidates. The selected model comes first, the
// configured default next when available, then remaining candidates by
// capability (and cost for ties). This retains the fail-safe preference while
// allowing a tertiary provider to recover when two backends fail.
func (c *Completer) resolve(eligible []int, index int, source string, score float64) decision {
	ordered := make([]int, 0, len(eligible))
	ordered = append(ordered, index)

	if index != c.defaultIndex {
		if slices.Contains(eligible, c.defaultIndex) {
			ordered = append(ordered, c.defaultIndex)
		}
	}

	remaining := make([]int, 0, len(eligible)-len(ordered))
	for _, i := range eligible {
		if slices.Contains(ordered, i) {
			continue
		}
		remaining = append(remaining, i)
	}

	slices.SortStableFunc(remaining, func(a, b int) int {
		ca := c.candidates[a]
		cb := c.candidates[b]

		switch {
		case ca.MaxDifficulty > cb.MaxDifficulty:
			return -1
		case ca.MaxDifficulty < cb.MaxDifficulty:
			return 1
		case ca.Cost < cb.Cost:
			return -1
		case ca.Cost > cb.Cost:
			return 1
		default:
			return 0
		}
	})

	ordered = append(ordered, remaining...)
	return decision{candidates: ordered, source: source, score: score}
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
