package sqlite_test

import (
	"strings"
	"sync"
	"testing"

	jetsqlite "github.com/go-jet/jet/v2/sqlite"

	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
)

func TestNullTextSerializesAsNull(t *testing.T) {
	t.Parallel()

	query, _ := jetsqlite.SELECT(sqliteent.NullText().AS("v")).Sql()
	if !strings.Contains(query, "CAST(NULL AS TEXT)") {
		t.Fatalf("NullText did not serialize to a NULL cast: %q", query)
	}
}

func TestNullTextIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	const goroutines = 8
	const iterations = 200

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range iterations {
				query, _ := jetsqlite.SELECT(sqliteent.NullText().AS("v")).Sql()
				if !strings.Contains(query, "CAST(NULL AS TEXT)") {
					t.Errorf("unstable serialization: %q", query)
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestNullBlobSerializesAsNull(t *testing.T) {
	t.Parallel()

	query, _ := jetsqlite.SELECT(sqliteent.NullBlob().AS("v")).Sql()
	if !strings.Contains(query, "CAST(NULL AS BLOB)") {
		t.Fatalf("NullBlob did not serialize to a NULL cast: %q", query)
	}
}

func TestNullBlobIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	const goroutines = 8
	const iterations = 200

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range iterations {
				query, _ := jetsqlite.SELECT(sqliteent.NullBlob().AS("v")).Sql()
				if !strings.Contains(query, "CAST(NULL AS BLOB)") {
					t.Errorf("unstable serialization: %q", query)
					return
				}
			}
		})
	}
	wg.Wait()
}
