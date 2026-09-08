package sheet

import (
	"bytes"
	"container/list"
	"context"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
)

var _ io.Closer = (*memorySnapshotStore)(nil)

type memoryEntry struct {
	key  SnapshotKey
	snap Snapshot
}

type memorySnapshotStore struct {
	maxBytes int64

	mu         sync.Mutex
	entries    map[SnapshotKey]*list.Element
	recency    *list.List
	totalBytes int64
}

func newMemorySnapshotStore(maxBytes int64) *memorySnapshotStore {
	return &memorySnapshotStore{
		maxBytes: maxBytes,
		entries:  make(map[SnapshotKey]*list.Element),
		recency:  list.New(),
	}
}

func (m *memorySnapshotStore) Get(_ context.Context, k SnapshotKey) (Snapshot, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	el, ok := m.entries[k]
	if !ok {
		return Snapshot{}, false, nil
	}
	m.recency.MoveToFront(el)

	// NOTE: the payload is copied in and out so a caller can neither observe nor cause a mutation of a cached entry.
	entry := memoryEntryOf(el)
	snap := entry.snap
	snap.Payload = bytes.Clone(entry.snap.Payload)
	return snap, true, nil
}

func (m *memorySnapshotStore) Put(_ context.Context, k SnapshotKey, s Snapshot, ttl time.Duration) error {
	size := int64(len(s.Payload))
	if size > m.maxBytes {
		return &SnapshotTooLargeError{Bytes: size, MaxBytes: m.maxBytes}
	}

	fetchedAt := s.FetchedAt.UTC()
	stored := Snapshot{
		ETag:      s.ETag,
		FetchedAt: fetchedAt,
		ExpiresAt: fetchedAt.Add(ttl),
		Payload:   bytes.Clone(s.Payload),
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.dropLocked(k)
	for oldest := m.recency.Back(); oldest != nil && m.totalBytes+size > m.maxBytes; oldest = m.recency.Back() {
		m.dropLocked(memoryEntryOf(oldest).key)
	}

	m.entries[k] = m.recency.PushFront(&memoryEntry{key: k, snap: stored})
	m.totalBytes += size
	return nil
}

func (m *memorySnapshotStore) PurgeSheet(_ context.Context, sheetID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k := range m.entries {
		if k.SheetID == sheetID {
			m.dropLocked(k)
		}
	}
	return nil
}

// Close drops every cached entry.
func (m *memorySnapshotStore) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries = make(map[SnapshotKey]*list.Element)
	m.recency.Init()
	m.totalBytes = 0
	return nil
}

func (m *memorySnapshotStore) dropLocked(k SnapshotKey) {
	el, ok := m.entries[k]
	if !ok {
		return
	}
	m.totalBytes -= int64(len(memoryEntryOf(el).snap.Payload))
	m.recency.Remove(el)
	delete(m.entries, k)
}

func memoryEntryOf(el *list.Element) *memoryEntry {
	entry, _ := el.Value.(*memoryEntry)
	return entry
}
