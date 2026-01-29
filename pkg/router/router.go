package router

import (
	"sync"
	"sync/atomic"
	"time"
)

// CircuitState represents the state of a circuit breaker
type CircuitState int

const (
	CircuitClosed   CircuitState = iota // Normal operation
	CircuitOpen                         // Failing, rejecting requests
	CircuitHalfOpen                     // Testing if recovered
)

// Default configuration values
const (
	DefaultFailureThreshold = 5
	DefaultRecoveryTimeout  = 30 * time.Second
)

// ProviderStats tracks health and performance metrics for a single provider
type ProviderStats struct {
	mu sync.RWMutex

	// Performance metrics
	avgTTFT       time.Duration // Time to first token (streaming responsiveness)
	totalRequests int64
	totalFailures int64

	// Atomic counter for inflight requests (lock-free for better performance)
	inflight atomic.Int64

	// Circuit breaker state
	state               CircuitState
	consecutiveFailures int
	lastFailure         time.Time
}

// NewProviderStats creates a new ProviderStats with default values
func NewProviderStats() *ProviderStats {
	return &ProviderStats{
		state:   CircuitClosed,
		avgTTFT: time.Second, // Initial estimate for time to first token
	}
}

// IsAvailable checks if the provider is available for requests
// Returns true if available, and transitions Open -> HalfOpen if recovery timeout has passed
func (s *ProviderStats) IsAvailable(recoveryTimeout time.Duration) bool {
	s.mu.RLock()
	state := s.state
	lastFailure := s.lastFailure
	s.mu.RUnlock()

	inflight := s.inflight.Load()

	switch state {
	case CircuitOpen:
		// Check if we should transition to half-open
		if time.Since(lastFailure) >= recoveryTimeout {
			s.mu.Lock()
			if s.state == CircuitOpen {
				s.state = CircuitHalfOpen
			}
			s.mu.Unlock()
			return true
		}
		return false

	case CircuitHalfOpen:
		// In half-open state, only allow if no inflight requests
		// This ensures we test recovery with a single request
		return inflight == 0

	default:
		return true
	}
}

// GetMetrics returns current metrics in a thread-safe manner
func (s *ProviderStats) GetMetrics() (state CircuitState, avgTTFT time.Duration, totalRequests, totalFailures, inflight int64) {
	s.mu.RLock()
	state = s.state
	avgTTFT = s.avgTTFT
	totalRequests = s.totalRequests
	totalFailures = s.totalFailures
	s.mu.RUnlock()

	inflight = s.inflight.Load()
	return
}

// RecordSuccess updates stats after a successful request
func (s *ProviderStats) RecordSuccess(ttft time.Duration, latencyAlpha float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.totalRequests++
	s.consecutiveFailures = 0

	// Update exponential moving average of TTFT (time to first token)
	if s.totalRequests == 1 {
		s.avgTTFT = ttft
	} else {
		// EMA: new_avg = alpha * new_value + (1 - alpha) * old_avg
		newAvg := float64(ttft)*latencyAlpha + float64(s.avgTTFT)*(1-latencyAlpha)
		s.avgTTFT = time.Duration(newAvg)
	}

	// Close circuit on success
	if s.state == CircuitHalfOpen {
		s.state = CircuitClosed
	}
}

// RecordFailure updates stats after a failed request
func (s *ProviderStats) RecordFailure(failureThreshold int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.totalRequests++
	s.totalFailures++
	s.consecutiveFailures++
	s.lastFailure = time.Now()

	// Open circuit if threshold reached or if we were in half-open state
	if s.state == CircuitHalfOpen || s.consecutiveFailures >= failureThreshold {
		s.state = CircuitOpen
	}
}

// GetLastFailure returns the last failure time in a thread-safe manner
func (s *ProviderStats) GetLastFailure() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastFailure
}

// SetHalfOpen transitions the circuit to half-open state
func (s *ProviderStats) SetHalfOpen() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = CircuitHalfOpen
}

// AddInflight increments the inflight counter and returns the new value
func (s *ProviderStats) AddInflight(delta int64) int64 {
	return s.inflight.Add(delta)
}

// GetInflight returns the current inflight count
func (s *ProviderStats) GetInflight() int64 {
	return s.inflight.Load()
}
