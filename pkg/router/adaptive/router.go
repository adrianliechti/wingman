package adaptive

import (
	"math/rand"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router"
)

// NewCompleter creates a smart router that scores providers by responsiveness
// (TTFT), error rate and current load, with circuit breaker protection and
// transparent failover
func NewCompleter(completers []provider.Completer, options ...router.Option) (*router.Completer, error) {
	return router.NewCompleter(completers, selectProvider, options...)
}

// explorationRate is the fraction of requests routed uniformly at random
// instead of by score. Without it a provider that warms up first dominates
// selection forever and cold or recovering providers never refresh their
// TTFT and error metrics.
const explorationRate = 0.1

// Compare two random healthy members. Sampling keeps the number of metrics
// snapshots constant even for large pools. Exploration gives recovering and
// idle backends a chance to refresh their observed latency.
func selectProvider(candidates []int, stats []*router.ProviderStats) int {
	if len(candidates) == 1 {
		return candidates[0]
	}

	a := rand.Intn(len(candidates))
	if rand.Float64() < explorationRate {
		return candidates[a]
	}
	b := (a + 1 + rand.Intn(len(candidates)-1)) % len(candidates)
	first, second := candidates[a], candidates[b]
	if cost(stats[second].Metrics()) < cost(stats[first].Metrics()) {
		return second
	}
	return first
}

func cost(m router.Metrics) float64 {
	ttft := max(m.TTFT.Milliseconds(), 1)
	cost := float64(ttft) * float64(1+m.Inflight) * (1 + 10*m.ErrorRate)
	if m.State != router.CircuitClosed {
		cost *= 10
	}
	return cost
}
