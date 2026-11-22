package fallback

import (
	"sync"
	"time"
)

// State represents the operational state of the circuit breaker.
type State int

const (
	StateClosed State = iota
	StateHalfOpen
	StateOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateHalfOpen:
		return "half-open"
	case StateOpen:
		return "open"
	default:
		return "unknown"
	}
}

// Clock abstracts time measurement for deterministic testing.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

// CircuitBreaker protects the rate limiter from hanging when Redis is unresponsive.
// It manages transitions across Closed, Open, and Half-Open states.
type CircuitBreaker struct {
	mu                  sync.Mutex
	state               State
	consecutiveFailures int
	threshold           int
	cooldown            time.Duration
	lastStateChange     time.Time
	halfOpenProbing     bool
	clock               Clock
}

// Option allows configuring optional parameters like an injectable Clock.
type Option func(*CircuitBreaker)

// WithClock injects a custom clock (used for deterministic testing).
func WithClock(c Clock) Option {
	return func(cb *CircuitBreaker) {
		// TODO: assign clock
		cb.clock = c
	}
}

// NewCircuitBreaker constructs a circuit breaker with a given failure threshold and cooldown duration.
func NewCircuitBreaker(threshold int, cooldown time.Duration, opts ...Option) *CircuitBreaker {
	// TODO: Initialize circuit breaker defaults and apply opts
	cb := &CircuitBreaker{
		state:               StateClosed,
		consecutiveFailures: 0,
		threshold:           threshold,
		cooldown:            cooldown,
		halfOpenProbing:     false,
		clock:               realClock{},
	}

	for _, opt := range opts {
		opt(cb)
	}

	cb.lastStateChange = cb.clock.Now()
	return cb
}

// AllowExecution determines whether a request to Redis should proceed.
// - In Closed: always true.
// - In Open: false if cooldown has not elapsed; if cooldown elapsed, transitions to Half-Open and allows 1 probe.
// - In Half-Open: true for the single canary probe; false for any concurrent callers while probe is pending.
func (cb *CircuitBreaker) AllowExecution() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == StateClosed {
		return true
	}

	if cb.state == StateOpen {
		if cb.clock.Now().Sub(cb.lastStateChange) >= cb.cooldown {
			cb.state = StateHalfOpen
			cb.halfOpenProbing = true
			cb.lastStateChange = cb.clock.Now()

			return true
		}

		return false
	}

	if cb.state == StateHalfOpen {
		if !cb.halfOpenProbing {
			cb.halfOpenProbing = true
			return true
		}
		return false
	}

	return false
}

// RecordSuccess signals that the downstream call (Redis) succeeded.
// - In Half-Open: resets failures and transitions to Closed.
// - In Closed: resets consecutive failure count to 0.
func (cb *CircuitBreaker) RecordSuccess() {
	// TODO: implement RecordSuccess
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateHalfOpen:
		cb.state = StateClosed
		cb.halfOpenProbing = false
		cb.consecutiveFailures = 0
		cb.lastStateChange = cb.clock.Now()
	case StateClosed:
		cb.consecutiveFailures = 0
	}
}

// RecordFailure signals that the downstream call (Redis) failed or timed out.
// - In Closed: increments consecutive failure count. If count >= threshold, transitions to Open.
// - In Half-Open: immediately trips back to Open and restarts cooldown.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		cb.consecutiveFailures++
		if cb.consecutiveFailures >= cb.threshold {
			cb.state = StateOpen
			cb.lastStateChange = cb.clock.Now()
		}
	case StateHalfOpen:
		cb.state = StateOpen
		cb.halfOpenProbing = false
		cb.lastStateChange = cb.clock.Now()
	}
}

// State returns the current state of the circuit breaker.
// If the breaker is Open but cooldown has elapsed, it reports StateHalfOpen.
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == StateOpen {
		if cb.clock.Now().Sub(cb.lastStateChange) >= cb.cooldown {
			cb.state = StateHalfOpen
			cb.lastStateChange = cb.clock.Now()
		}
	}

	return cb.state
}
