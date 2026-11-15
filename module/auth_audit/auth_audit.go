// Package auth_audit is the append-only audit trail of the login framework: every login, failure, logout,
// session revocation, credential and photo change is written to a Sink. The service points the Sink at its own
// audit store; Memory is for tests.
package auth_audit

import (
	"context"
	"sync"
	"time"
)

// Kind names what happened.
type Kind string

const (
	LoginSucceeded       Kind = "login_succeeded"
	LoginFailed          Kind = "login_failed"
	Logout               Kind = "logout"
	SessionRotated       Kind = "session_rotated"
	SessionRevoked       Kind = "session_revoked"  // one session ended by its user from another session
	SessionsRevoked      Kind = "sessions_revoked" // every session of a user ended (disabled, deleted, fail closed)
	ProfilePropagated    Kind = "profile_propagated"
	PropagationFailed    Kind = "propagation_failed"
	CredentialChanged    Kind = "credential_changed"
	CredentialRegistered Kind = "credential_registered"
	CredentialRemoved    Kind = "credential_removed"
	PhotoChanged         Kind = "photo_changed"
	PhotoRemoved         Kind = "photo_removed"
)

// Outcome is whether the step did what it was asked to.
type Outcome string

const (
	OutcomeOK     Outcome = "ok"
	OutcomeFailed Outcome = "failed"
)

// Event is one audit record. It never holds a secret: no password, session token, CSRF token or credential.
type Event struct {
	Time time.Time
	Kind Kind
	// UserUUID is the user the event is about.
	UserUUID string
	// ActorUUID is who caused it when that is not the user (an administrator); empty otherwise.
	ActorUUID string
	// SessionID is the session's public id (a hash of its token, never the token).
	SessionID string
	Outcome   Outcome
	// Reason is the real cause on a failure: the operation and the underlying error text.
	Reason string
	// Detail holds further facts as text (auth method, client address, counts).
	Detail map[string]string
}

// Sink writes audit events to an append-only store. Write must not return before the event is stored, and must
// return the store's own error when it is not.
type Sink interface {
	Write(ctx context.Context, e Event) error
}

// Memory is a Sink that keeps events in memory, for tests.
type Memory struct {
	mu     sync.Mutex
	events []Event
	// Fail, when set, is returned by Write instead of storing the event.
	Fail error
}

// Write stores the event, or returns Fail.
func (m *Memory) Write(_ context.Context, e Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	m.events = append(m.events, e)
	return nil
}

// Events returns a copy of the stored events.
func (m *Memory) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out
}

// OfKind returns the stored events of one kind.
func (m *Memory) OfKind(k Kind) []Event {
	var out []Event
	for _, e := range m.Events() {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}
