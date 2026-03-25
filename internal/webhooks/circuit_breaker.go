package webhooks

import (
	"expvar"
	"sync"
	"time"
)

// cbState is the state of a per-subscription circuit breaker.
type cbState int

const (
	cbClosed   cbState = iota // normal — deliveries pass through
	cbOpen                    // tripped — deliveries are fast-failed until cooldown elapses
	cbHalfOpen                // cooldown elapsed — one trial delivery is permitted
)

// metricsCBOpensTotal counts the number of times any circuit breaker trips to
// the Open state. It is a process-lifetime monotonic counter.
var metricsCBOpensTotal = expvar.NewInt("webhook_cb_opens_total")

// CircuitBreakerConfig holds the parameters for per-subscription circuit breaking.
// Set Config.CircuitBreaker to a non-nil pointer to enable the feature.
type CircuitBreakerConfig struct {
	// FailureThreshold is the number of consecutive exhausted-delivery failures
	// (i.e. all retry attempts used up with no success) required to trip the
	// breaker into the Open state.  When ≤ 0, the package default (5) is used.
	FailureThreshold int

	// OpenDuration is how long the breaker remains in the Open state before
	// transitioning to Half-Open and allowing a single trial delivery.
	// When ≤ 0, the package default (60 s) is used.
	OpenDuration time.Duration
}

const (
	defaultCBFailureThreshold = 5
	defaultCBOpenDuration     = 60 * time.Second
)

// circuitBreaker is a concurrency-safe three-state circuit breaker for a
// single webhook subscription.
type circuitBreaker struct {
	mu           sync.Mutex
	state        cbState
	failures     int       // consecutive exhausted-delivery failure count
	openedAt     time.Time // when the breaker last transitioned to Open
	threshold    int
	openDuration time.Duration
}

func newCircuitBreaker(cfg CircuitBreakerConfig) *circuitBreaker {
	t := cfg.FailureThreshold
	if t <= 0 {
		t = defaultCBFailureThreshold
	}
	d := cfg.OpenDuration
	if d <= 0 {
		d = defaultCBOpenDuration
	}
	return &circuitBreaker{threshold: t, openDuration: d}
}

// Allow reports whether the next delivery attempt should proceed.
//
//   - Closed  → always true.
//   - Open    → false until the cooldown elapses; then transitions to
//     Half-Open and returns true for the single trial.
//   - HalfOpen → false (a trial is already in progress; do not allow a
//     second concurrent probe).
func (cb *circuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case cbClosed:
		return true
	case cbOpen:
		if time.Since(cb.openedAt) < cb.openDuration {
			return false
		}
		// Cooldown has elapsed — allow one trial delivery.
		cb.state = cbHalfOpen
		return true
	case cbHalfOpen:
		// A trial is already in progress; reject concurrent callers.
		return false
	}
	return true
}

// RecordSuccess resets the breaker to Closed and clears the failure counter.
func (cb *circuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = cbClosed
	cb.failures = 0
}

// RecordFailure increments the consecutive failure counter.  When the counter
// reaches the threshold (or the breaker is in Half-Open, meaning the trial
// delivery also failed), the breaker transitions to Open.
func (cb *circuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	if cb.state == cbHalfOpen || cb.failures >= cb.threshold {
		if cb.state != cbOpen {
			metricsCBOpensTotal.Add(1)
		}
		cb.state = cbOpen
		cb.openedAt = time.Now()
	}
}

// breakerRegistry is a concurrency-safe registry of per-subscription circuit
// breakers.  It lazily creates a new breaker on the first lookup for any
// subscription ID.
type breakerRegistry struct {
	cfg CircuitBreakerConfig
	m   sync.Map // string → *circuitBreaker
}

func newBreakerRegistry(cfg CircuitBreakerConfig) *breakerRegistry {
	return &breakerRegistry{cfg: cfg}
}

// get returns the circuitBreaker for subscriptionID, creating one lazily.
func (br *breakerRegistry) get(subscriptionID string) *circuitBreaker {
	if v, ok := br.m.Load(subscriptionID); ok {
		return v.(*circuitBreaker)
	}
	cb := newCircuitBreaker(br.cfg)
	if actual, loaded := br.m.LoadOrStore(subscriptionID, cb); loaded {
		return actual.(*circuitBreaker)
	}
	return cb
}
