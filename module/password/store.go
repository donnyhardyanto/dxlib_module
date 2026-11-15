package password

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Record is one user's stored password.
type Record struct {
	UserUUID string
	// Ciphertext is the Transit ciphertext of the user's uuid and Argon2id hash.
	Ciphertext string
	// KeyVersion is the transit key version that made Ciphertext.
	KeyVersion int
	// MustChange is set when someone other than the user chose the password (must_change_password).
	MustChange bool
	ChangedAt  time.Time
}

// Store keeps the passwords. Every method that names a user only touches that user's row.
type Store interface {
	// Get returns the user's record. ErrNotFound when the user has no password.
	Get(ctx context.Context, userUUID string) (*Record, error)
	// Put stores the record, replacing the user's earlier one. ErrConflict when another Put for the same user
	// stored a first password at the same moment.
	Put(ctx context.Context, r Record) error
	// Swap replaces the user's record only while its ciphertext is still oldCiphertext (a rewrap or a rehash must
	// not undo a password change made meanwhile). ErrConflict when it is not; ErrNotFound when there is no record.
	Swap(ctx context.Context, oldCiphertext string, r Record) error
	// ByKeyVersion returns up to limit records encrypted with this key version.
	ByKeyVersion(ctx context.Context, version, limit int) ([]Record, error)
	// Remove deletes the user's record. Before deleting, and while no other change of the user's record can run,
	// it calls allow; an error from allow refuses the removal and is returned. allow may be nil. ErrNotFound when
	// there is no record.
	Remove(ctx context.Context, userUUID string, allow func() error) error
}

// MemoryStore is a Store in memory, for tests.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]Record
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore { return &MemoryStore{rows: map[string]Record{}} }

// Get returns the user's record.
func (s *MemoryStore) Get(_ context.Context, userUUID string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[userUUID]
	if !ok {
		return nil, ErrNotFound
	}
	return &r, nil
}

// Put stores the record.
func (s *MemoryStore) Put(_ context.Context, r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[r.UserUUID] = r
	return nil
}

// Swap replaces the record while its ciphertext is unchanged.
func (s *MemoryStore) Swap(_ context.Context, oldCiphertext string, r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.rows[r.UserUUID]
	if !ok {
		return ErrNotFound
	}
	if cur.Ciphertext != oldCiphertext {
		return ErrConflict
	}
	s.rows[r.UserUUID] = r
	return nil
}

// ByKeyVersion returns up to limit records of the key version, by user uuid.
func (s *MemoryStore) ByKeyVersion(_ context.Context, version, limit int) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Record
	for _, r := range s.rows {
		if r.KeyVersion == version {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserUUID < out[j].UserUUID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Remove deletes the record after allow agrees.
func (s *MemoryStore) Remove(_ context.Context, userUUID string, allow func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[userUUID]; !ok {
		return ErrNotFound
	}
	if allow != nil {
		if err := allow(); err != nil {
			return err
		}
	}
	delete(s.rows, userUUID)
	return nil
}

var _ Store = (*MemoryStore)(nil)
