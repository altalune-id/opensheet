package sheet

// NewMemorySnapshotStoreForTest exposes the memory driver to the external test package.
func NewMemorySnapshotStoreForTest(maxBytes int64) SnapshotStore {
	return newMemorySnapshotStore(maxBytes)
}

// NewPostgresSnapshotStoreForTest exposes a postgres driver with no connection, for code that must never touch it.
func NewPostgresSnapshotStoreForTest() SnapshotStore {
	return newPostgresSnapshotStore(nil, "", "")
}
