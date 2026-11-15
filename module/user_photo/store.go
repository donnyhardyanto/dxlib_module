package user_photo

import (
	"context"
	"sync"
	"time"
)

// Record is one user's row: which object holds their photo.
type Record struct {
	UserUUID string
	// ObjectKey names the photo's object in the bucket; empty once the photo was removed (the row stays, so the
	// version keeps rising).
	ObjectKey string
	// Version rises by one with every change or removal.
	Version   int64
	ChangedAt time.Time
}

// Store keeps the users' photo rows. Every method that names a user touches only that user's row.
type Store interface {
	// Get returns the user's row. ErrNotFound when the user has none.
	Get(ctx context.Context, userUUID string) (*Record, error)
	// Replace points the user's row at objectKey with the next version and returns the row before (nil when there
	// was none) and after, both read under the row's lock. An empty objectKey removes the photo, and gives
	// ErrNotFound when the user has none to remove. ErrConflict when another Replace stored the user's first row
	// at the same moment.
	Replace(ctx context.Context, userUUID, objectKey string, at time.Time) (before *Record, after Record, err error)
	// Delete removes the user's row and returns it. ErrNotFound when there is none.
	Delete(ctx context.Context, userUUID string) (*Record, error)
}

// MemoryStore is a Store in memory, for tests.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]Record
	// Fail, when set, is returned by every method instead of doing anything.
	Fail error
	// Calls counts the calls of every method.
	Calls int
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore { return &MemoryStore{rows: map[string]Record{}} }

// Get returns the user's row.
func (s *MemoryStore) Get(_ context.Context, userUUID string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls++
	if s.Fail != nil {
		return nil, s.Fail
	}
	r, ok := s.rows[userUUID]
	if !ok {
		return nil, ErrNotFound
	}
	return &r, nil
}

// Replace points the row at objectKey with the next version.
func (s *MemoryStore) Replace(_ context.Context, userUUID, objectKey string, at time.Time) (*Record, Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls++
	if s.Fail != nil {
		return nil, Record{}, s.Fail
	}
	var before *Record
	next := Record{UserUUID: userUUID, ObjectKey: objectKey, Version: 1, ChangedAt: at.UTC()}
	if cur, ok := s.rows[userUUID]; ok {
		c := cur
		before = &c
		next.Version = cur.Version + 1
	}
	if objectKey == "" && (before == nil || before.ObjectKey == "") {
		return nil, Record{}, ErrNotFound
	}
	s.rows[userUUID] = next
	return before, next, nil
}

// Delete removes the row.
func (s *MemoryStore) Delete(_ context.Context, userUUID string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls++
	if s.Fail != nil {
		return nil, s.Fail
	}
	r, ok := s.rows[userUUID]
	if !ok {
		return nil, ErrNotFound
	}
	delete(s.rows, userUUID)
	return &r, nil
}

var _ Store = (*MemoryStore)(nil)
