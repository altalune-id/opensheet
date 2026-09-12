package sheet

// NewMemorySnapshotStoreForTest exposes the memory driver to the external test package.
func NewMemorySnapshotStoreForTest(maxBytes int64) SnapshotStore {
	return newMemorySnapshotStore(maxBytes)
}
