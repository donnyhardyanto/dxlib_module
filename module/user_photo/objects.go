package user_photo

import (
	"context"
	"sync"
)

// ObjectStore keeps the photo files in a private bucket.
type ObjectStore interface {
	// Put stores data under key.
	Put(ctx context.Context, key string, data []byte, contentType string) error
	// Get returns the object's bytes, at most maxBytes of them. ErrNotFound when there is no such object.
	Get(ctx context.Context, key string, maxBytes int64) ([]byte, error)
	// Delete removes the object; nil when it is already gone.
	Delete(ctx context.Context, key string) error
}

// MemoryObjectStore is an ObjectStore in memory, for tests.
type MemoryObjectStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	// FailPut, FailGet and FailDelete, when set, are returned by that method instead of doing anything.
	FailPut, FailGet, FailDelete error
	// Calls counts the calls of every method.
	Calls int
}

// NewMemoryObjectStore returns an empty MemoryObjectStore.
func NewMemoryObjectStore() *MemoryObjectStore {
	return &MemoryObjectStore{objects: map[string][]byte{}}
}

// Put stores a copy of data.
func (s *MemoryObjectStore) Put(_ context.Context, key string, data []byte, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls++
	if s.FailPut != nil {
		return s.FailPut
	}
	s.objects[key] = append([]byte(nil), data...)
	return nil
}

// Get returns a copy of the object.
func (s *MemoryObjectStore) Get(_ context.Context, key string, _ int64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls++
	if s.FailGet != nil {
		return nil, s.FailGet
	}
	b, ok := s.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), b...), nil
}

// Delete removes the object.
func (s *MemoryObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls++
	if s.FailDelete != nil {
		return s.FailDelete
	}
	delete(s.objects, key)
	return nil
}

// Keys returns the stored keys.
func (s *MemoryObjectStore) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.objects))
	for k := range s.objects {
		out = append(out, k)
	}
	return out
}

var _ ObjectStore = (*MemoryObjectStore)(nil)
