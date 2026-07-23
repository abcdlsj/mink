package websession

import (
	"sync"
	"time"
)

const (
	loginFailureWindow = 5 * time.Minute
	loginBlockDuration = time.Minute
	loginFailureLimit  = 5
)

type loginFailureState struct {
	count        int
	windowStart  time.Time
	blockedUntil time.Time
}

type loginFailureGuard struct {
	mu       sync.Mutex
	failures map[string]loginFailureState
}

func newLoginFailureGuard() *loginFailureGuard {
	return &loginFailureGuard{failures: make(map[string]loginFailureState)}
}

func (guard *loginFailureGuard) allowed(key string, now time.Time) bool {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	state, ok := guard.failures[key]
	if !ok {
		return true
	}
	if !state.blockedUntil.IsZero() && now.Before(state.blockedUntil) {
		return false
	}
	if now.Sub(state.windowStart) >= loginFailureWindow {
		delete(guard.failures, key)
	}
	return true
}

func (guard *loginFailureGuard) failed(key string, now time.Time) {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	state, ok := guard.failures[key]
	if !ok || now.Sub(state.windowStart) >= loginFailureWindow {
		state = loginFailureState{windowStart: now}
	}
	state.count++
	if state.count >= loginFailureLimit {
		state.blockedUntil = now.Add(loginBlockDuration)
	}
	guard.failures[key] = state
}

func (guard *loginFailureGuard) succeeded(key string) {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	delete(guard.failures, key)
}
