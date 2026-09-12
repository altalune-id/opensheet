package sheet

import (
	"bytes"
	"context"
	"sync"
	"time"
)

type memoryAttempt struct {
	attempt   Attempt
	expiresAt time.Time
}

type memoryIdempotencyStore struct {
	mu      sync.Mutex
	entries map[IdempotencyKey]memoryAttempt
}

// NewMemoryIdempotencyStore returns the per-process driver, which is the one every non-postgres database gets.
func NewMemoryIdempotencyStore() IdempotencyStore {
	return &memoryIdempotencyStore{entries: make(map[IdempotencyKey]memoryAttempt)}
}

func (m *memoryIdempotencyStore) Reserve(
	_ context.Context, k IdempotencyKey, bodyHash string, ttl time.Duration,
) (bool, Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if e, ok := m.entries[k]; ok {
		return false, e.attempt.clone(), nil
	}
	now := time.Now().UTC()
	e := memoryAttempt{
		attempt:   Attempt{BodyHash: bodyHash, CreatedAt: now},
		expiresAt: now.Add(ttl),
	}
	m.entries[k] = e
	return true, e.attempt.clone(), nil
}

func (m *memoryIdempotencyStore) Complete(
	_ context.Context, k IdempotencyKey, payload []byte, ttl time.Duration,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.entries[k]
	if !ok {
		return nil
	}
	e.attempt.Payload = bytes.Clone(payload)
	e.attempt.Done = true
	e.expiresAt = time.Now().UTC().Add(ttl)
	m.entries[k] = e
	return nil
}

func (m *memoryIdempotencyStore) Release(_ context.Context, k IdempotencyKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.entries, k)
	return nil
}

// NOTE: this driver holds no org_id, so a ScopeTenant sweep clears every tenant's expired entries and reports the global count; expiry is not tenant data, so that is safe but the count differs from the postgres driver's.
func (m *memoryIdempotencyStore) DeleteExpired(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	deleted := 0
	for k, e := range m.entries {
		if e.expiresAt.After(now) {
			continue
		}
		delete(m.entries, k)
		deleted++
	}
	return deleted, nil
}
