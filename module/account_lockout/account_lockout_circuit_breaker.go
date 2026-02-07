package account_lockout

import (
	"sync"
	"time"
)

type CircuitBreakerState int

const (
	StateClosed CircuitBreakerState = iota
	StateOpen
	StateHalfOpen
)

type CircuitBreaker struct {
	threshold       int
	timeout         time.Duration
	failureCount    int
	lastFailureTime time.Time
	state           CircuitBreakerState
	mutex           sync.RWMutex
}

func NewCircuitBreaker(threshold int, timeoutSeconds int) *CircuitBreaker {
	return &CircuitBreaker{
		threshold: threshold,
		timeout:   time.Duration(timeoutSeconds) * time.Second,
		state:     StateClosed,
	}
}

func (cb *CircuitBreaker) CanExecute() bool {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	// If closed, allow execution
	if cb.state == StateClosed {
		return true
	}

	// If open, check if timeout elapsed
	if cb.state == StateOpen {
		if time.Since(cb.lastFailureTime) > cb.timeout {
			// Transition to half-open
			cb.mutex.RUnlock()
			cb.mutex.Lock()
			cb.state = StateHalfOpen
			cb.mutex.Unlock()
			cb.mutex.RLock()
			return true
		}
		return false
	}

	// Half-open state - allow one request
	return true
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.failureCount = 0
	if cb.state == StateHalfOpen {
		cb.state = StateClosed
	}
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.failureCount++
	cb.lastFailureTime = time.Now()

	if cb.failureCount >= cb.threshold {
		cb.state = StateOpen
	}
}

func (cb *CircuitBreaker) GetState() CircuitBreakerState {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()
	return cb.state
}
