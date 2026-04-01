package openai

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// throttleTransport is an http.RoundTripper that reads rate-limit headers from
// every response and pauses before the next request when remaining capacity is low.
// This prevents hitting 429s proactively rather than relying on retry-after-the-fact.
type throttleTransport struct {
	base http.RoundTripper

	mu        sync.Mutex
	waitUntil time.Time
}

func newThrottleTransport(base http.RoundTripper) *throttleTransport {
	if base == nil {
		base = http.DefaultTransport
	}

	return &throttleTransport{base: base}
}

func (t *throttleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	wait := time.Until(t.waitUntil)
	t.mu.Unlock()

	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()

		select {
		case <-timer.C:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}

	t.observe(resp.Header)

	return resp, nil
}

func (t *throttleTransport) observe(h http.Header) {
	var delay time.Duration

	// Check requests and tokens independently — they have completely
	// different scales (e.g. 59 remaining requests vs 149984 remaining tokens)
	// so each must be compared against its own reset duration.
	//
	//   x-ratelimit-remaining-requests: 59    x-ratelimit-reset-requests: 1s
	//   x-ratelimit-remaining-tokens: 149984  x-ratelimit-reset-tokens:   6m0s
	if d := checkLimit(h, "requests"); d > delay {
		delay = d
	}

	if d := checkLimit(h, "tokens"); d > delay {
		delay = d
	}

	if delay == 0 {
		return
	}

	if delay > 60*time.Second {
		delay = 60 * time.Second
	}

	t.mu.Lock()
	if until := time.Now().Add(delay); until.After(t.waitUntil) {
		t.waitUntil = until
	}
	t.mu.Unlock()
}

// checkLimit checks a single rate-limit dimension (requests or tokens).
// Returns the reset duration if remaining is low, otherwise 0.
func checkLimit(h http.Header, dimension string) time.Duration {
	remaining := headerInt(h.Get("x-ratelimit-remaining-" + dimension))

	// Negative means header absent or invalid (e.g. -1 from Responses API bug).
	if remaining < 0 {
		return 0
	}

	limit := headerInt(h.Get("x-ratelimit-limit-" + dimension))

	if !isLow(remaining, limit) {
		return 0
	}

	delay := headerDuration(h.Get("x-ratelimit-reset-" + dimension))

	if delay == 0 {
		delay = 1 * time.Second
	}

	return delay
}

// isLow returns true if the remaining capacity is critically low relative to the limit.
func isLow(remaining, limit int) bool {
	if remaining <= 1 {
		return true
	}

	// If we know the limit, trigger when less than 5% remains.
	if limit > 0 {
		return remaining < limit/20
	}

	return false
}

func headerInt(v string) int {
	if v == "" {
		return -1
	}

	n, err := strconv.Atoi(v)
	if err != nil {
		return -1
	}

	return n
}

func headerDuration(v string) time.Duration {
	if v == "" {
		return 0
	}

	if d, err := time.ParseDuration(v); err == nil {
		return d
	}

	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		return time.Duration(secs * float64(time.Second))
	}

	return 0
}
