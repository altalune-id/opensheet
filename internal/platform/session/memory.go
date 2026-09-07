package session

import (
	"context"
	"sync"
	"time"
)

type MemoryStore struct {
	mu   sync.RWMutex
	rows map[string]memRow
}

type memRow struct {
	p   Principal
	exp time.Time
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{rows: map[string]memRow{}} }

func (s *MemoryStore) Save(_ context.Context, sid string, p Principal, exp time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[sid] = memRow{p: p, exp: exp}
	return nil
}

func (s *MemoryStore) Load(_ context.Context, sid string) (Principal, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rows[sid]
	if !ok {
		return Principal{}, false, nil
	}
	if time.Now().After(r.exp) {
		return Principal{}, false, nil
	}
	return r.p, true, nil
}

func (s *MemoryStore) Delete(_ context.Context, sid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, sid)
	return nil
}
