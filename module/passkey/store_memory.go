package passkey

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryStore is a CredentialStore in memory, for tests.
type MemoryStore struct {
	mu   sync.Mutex
	byID map[string]Passkey
	// Fail, when set, is returned by every method.
	Fail error
}

var _ CredentialStore = (*MemoryStore)(nil)

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{byID: map[string]Passkey{}} }

func clonePasskey(p Passkey) Passkey {
	p.Credential = append([]byte(nil), p.Credential...)
	return p
}

func (m *MemoryStore) countOf(userUUID string) int {
	n := 0
	for _, p := range m.byID {
		if p.UserUUID == userUUID {
			n++
		}
	}
	return n
}

// Add stores a new passkey.
func (m *MemoryStore) Add(_ context.Context, p Passkey, maxPerUser int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	if _, ok := m.byID[p.ID]; ok {
		return ErrDuplicate
	}
	if maxPerUser > 0 && m.countOf(p.UserUUID) >= maxPerUser {
		return ErrTooMany
	}
	m.byID[p.ID] = clonePasskey(p)
	return nil
}

// ByUser returns the user's passkeys, oldest first.
func (m *MemoryStore) ByUser(_ context.Context, userUUID string) ([]Passkey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	var out []Passkey
	for _, p := range m.byID {
		if p.UserUUID == userUUID {
			out = append(out, clonePasskey(p))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// ByID returns the passkey with this ID.
func (m *MemoryStore) ByID(_ context.Context, id string) (*Passkey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	p, ok := m.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := clonePasskey(p)
	return &c, nil
}

// RecordUse stores the credential record and time after a login.
func (m *MemoryStore) RecordUse(_ context.Context, userUUID, id string, credential []byte, usedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	p, ok := m.byID[id]
	if !ok || p.UserUUID != userUUID {
		return ErrNotFound
	}
	p.Credential = append([]byte(nil), credential...)
	p.LastUsedAt = usedAt
	m.byID[id] = p
	return nil
}

// Remove deletes one of the user's passkeys, after allow agrees.
func (m *MemoryStore) Remove(_ context.Context, userUUID, id string, allow func(remaining int) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	p, ok := m.byID[id]
	if !ok || p.UserUUID != userUUID {
		return ErrNotFound
	}
	if allow != nil {
		if err := allow(m.countOf(userUUID) - 1); err != nil {
			return err
		}
	}
	delete(m.byID, id)
	return nil
}
