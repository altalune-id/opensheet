package webhook_test

import (
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/events"
	"altalune.id/opensheet/internal/platform/outbox"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/webhook"
	"altalune.id/opensheet/schema"
)

const prefix = "opensheet_"

func newSQLiteStore(t *testing.T) (webhook.Store, *sql.DB, tenant.Context) {
	t.Helper()
	cfg := config.Defaults()
	cfg.DB.Driver = db.DriverSQLite
	// NOTE: a temp-file DSN, not :memory:, or pooled connections do not share the database and cascades never fire.
	cfg.DB.DSN = filepath.Join(t.TempDir(), "webhook.db")

	sqlDB, err := db.Open(t.Context(), cfg.DB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))
	require.Equal(t, prefix, cfg.DB.TablePrefix)

	store := webhook.NewStore(
		db.DBConfig{Driver: db.DriverSQLite, TablePrefix: prefix},
		db.Pool{W: sqlDB, R: sqlDB},
		nil,
	)
	return store, sqlDB, seedTenant(t, sqlDB)
}

func seedTenant(t *testing.T, sqlDB *sql.DB) tenant.Context {
	t.Helper()
	userID, orgID := uuid.New(), uuid.New()
	now := sqliteent.SQLiteTime(time.Now())
	_, err := sqlDB.Exec(
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES (?, ?, '', '', 0, ?, ?)",
		userID.String(), userID.String()+"@x.com", now, now)
	require.NoError(t, err)
	_, err = sqlDB.Exec(
		"INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES (?, ?, 'Org', ?, ?, ?)",
		orgID.String(), orgID.String()[:8], userID.String(), now, now)
	require.NoError(t, err)
	tc := tenant.Context{OrgID: orgID, UserID: userID}
	tc.ProjectID = seedProject(t, sqlDB, tc)
	return tc
}

func seedProject(t *testing.T, sqlDB *sql.DB, tc tenant.Context) uuid.UUID {
	t.Helper()
	projID := uuid.New()
	now := sqliteent.SQLiteTime(time.Now())
	_, err := sqlDB.Exec(
		"INSERT INTO "+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES (?, ?, ?, 'Web', ?, ?, ?)",
		projID.String(), tc.OrgID.String(), projID.String()[:8], tc.UserID.String(), now, now)
	require.NoError(t, err)
	return projID
}

func newSealedEndpoint(t *testing.T, tc tenant.Context, types ...events.Type) *webhook.Endpoint {
	t.Helper()
	if len(types) == 0 {
		types = []events.Type{events.PostPublished, events.PostDeleted}
	}
	e, err := webhook.New(tc.OrgID, tc.ProjectID, validURL, "orders", types)
	require.NoError(t, err)
	e.Secrets = webhook.SealedSecrets{Primary: []byte("sealed-primary")}
	e.CreatedAt = e.CreatedAt.Truncate(time.Microsecond)
	e.UpdatedAt = e.CreatedAt
	return e
}

// NOTE: timestamptz keeps microseconds, so a fixture time must not carry nanoseconds a round trip would drop.
func updateEndpoint(t *testing.T, e *webhook.Endpoint, rawURL, description string, types []events.Type, active bool) {
	t.Helper()
	require.NoError(t, e.Update(rawURL, description, types, active))
	e.UpdatedAt = e.UpdatedAt.Truncate(time.Microsecond)
}

func newAttempt(tc tenant.Context, endpointID, deliveryID uuid.UUID, n int, at time.Time) webhook.Attempt {
	return webhook.Attempt{
		ID:         uuid.Must(uuid.NewV7()),
		OrgID:      tc.OrgID,
		ProjectID:  tc.ProjectID,
		EndpointID: endpointID,
		DeliveryID: deliveryID,
		EventID:    uuid.New(),
		EventType:  events.PostPublished,
		Attempt:    n,
		StatusCode: 500,
		Error:      "boom",
		Duration:   1234 * time.Millisecond,
		CreatedAt:  at,

		ResponseBody:      `{"error":"<b>x</b>"}`,
		ResponseTruncated: true,
		ResponseHeaders:   []webhook.Header{{Name: "Content-Type", Value: "application/json"}, {Name: "X-Request-Id", Value: "req_1"}},
	}
}

func assertSameResponse(t *testing.T, want, got webhook.Attempt) {
	t.Helper()
	assert.Equal(t, want.ResponseBody, got.ResponseBody)
	assert.Equal(t, want.ResponseTruncated, got.ResponseTruncated)
	assert.Equal(t, want.ResponseHeaders, got.ResponseHeaders)
}

func noResponse(a webhook.Attempt) webhook.Attempt {
	a.StatusCode = 0
	a.Error = "dial tcp: connection refused"
	a.ResponseBody = ""
	a.ResponseTruncated = false
	a.ResponseHeaders = nil
	return a
}

func assertSameEndpoint(t *testing.T, want, got *webhook.Endpoint) {
	t.Helper()
	assert.Equal(t, want.ID, got.ID)
	assert.Equal(t, want.OrgID, got.OrgID)
	assert.Equal(t, want.ProjectID, got.ProjectID)
	assert.Equal(t, want.URL, got.URL)
	assert.Equal(t, want.Description, got.Description)
	assert.Equal(t, want.EventTypes, got.EventTypes)
	assert.Equal(t, want.Secrets, got.Secrets)
	assert.Equal(t, want.Active, got.Active)
	assert.True(t, want.CreatedAt.Equal(got.CreatedAt), "created_at: want %v got %v", want.CreatedAt, got.CreatedAt)
	assert.True(t, want.UpdatedAt.Equal(got.UpdatedAt), "updated_at: want %v got %v", want.UpdatedAt, got.UpdatedAt)
	assert.Equal(t, time.UTC, got.CreatedAt.Location())
}

func TestSQLite_SaveAndByID(t *testing.T) {
	store, _, tc := newSQLiteStore(t)
	ctx := tenant.Into(t.Context(), tc)

	e := newSealedEndpoint(t, tc)
	require.NoError(t, store.Save(ctx, e))

	got, err := store.ByID(ctx, e.ID)
	require.NoError(t, err)
	assertSameEndpoint(t, e, got)
	assert.Nil(t, got.Secrets.Secondary, "a NULL secondary must read back as nil")
}

func TestSQLite_SaveWritesAnEmptySecondaryAsNull(t *testing.T) {
	store, sqlDB, tc := newSQLiteStore(t)
	ctx := tenant.Into(t.Context(), tc)

	e := newSealedEndpoint(t, tc)
	e.Secrets.Secondary = []byte{}
	require.NoError(t, store.Save(ctx, e), "an empty secondary must not trip the length CHECK on insert")
	require.NoError(t, store.Save(ctx, e), "nor on the conflict update")

	got, err := store.ByID(ctx, e.ID)
	require.NoError(t, err)
	assert.Nil(t, got.Secrets.Secondary)
	var nulls int
	require.NoError(t, sqlDB.QueryRow("SELECT count(*) FROM "+prefix+"webhook_endpoints WHERE id = ? AND secret_secondary IS NULL", e.ID.String()).Scan(&nulls))
	assert.Equal(t, 1, nulls)
}

func TestSQLite_SaveAttemptForADeletedEndpointIsNotFound(t *testing.T) {
	store, _, tc := newSQLiteStore(t)
	ctx := tenant.Into(t.Context(), tc)
	e := newSealedEndpoint(t, tc)
	require.NoError(t, store.Save(ctx, e))
	require.NoError(t, store.Delete(ctx, e.ID))

	err := store.SaveAttempt(ctx, newAttempt(tc, e.ID, uuid.New(), 1, time.Now()))
	assert.True(t, webhook.IsNotFoundError(err), "the endpoint FK must surface as NotFoundError, got %T: %v", err, err)
}

func TestSQLite_ByID_NotFound(t *testing.T) {
	store, _, tc := newSQLiteStore(t)
	_, err := store.ByID(tenant.Into(t.Context(), tc), uuid.New())
	assert.True(t, webhook.IsNotFoundError(err), "got %T: %v", err, err)
}

func TestSQLite_TenantMissing(t *testing.T) {
	store, _, tc := newSQLiteStore(t)
	e := newSealedEndpoint(t, tc)

	assert.Error(t, store.Save(t.Context(), e))
	_, err := store.ByID(t.Context(), e.ID)
	assert.Error(t, err)
	_, err = store.List(t.Context(), tc.OrgID, tc.ProjectID)
	assert.Error(t, err)
	assert.Error(t, store.Delete(t.Context(), e.ID))
	assert.Error(t, store.SaveAttempt(t.Context(), newAttempt(tc, e.ID, uuid.New(), 1, time.Now())))
	_, err = store.ListAttempts(t.Context(), e.ID, uuid.New())
	assert.Error(t, err)
}

func TestSQLite_List_OrdersByCreatedAtThenID(t *testing.T) {
	store, sqlDB, tc := newSQLiteStore(t)
	ctx := tenant.Into(t.Context(), tc)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	older := newSealedEndpoint(t, tc)
	older.CreatedAt = base
	tieA := newSealedEndpoint(t, tc)
	tieA.CreatedAt = base.Add(time.Minute)
	tieB := newSealedEndpoint(t, tc)
	tieB.CreatedAt = base.Add(time.Minute)
	for _, e := range []*webhook.Endpoint{older, tieA, tieB} {
		require.NoError(t, store.Save(ctx, e))
	}

	otherProject := tc
	otherProject.ProjectID = seedProject(t, sqlDB, tc)
	require.NoError(t, store.Save(tenant.Into(t.Context(), otherProject), newSealedEndpoint(t, otherProject)))

	got, err := store.List(ctx, tc.OrgID, tc.ProjectID)
	require.NoError(t, err)
	require.Len(t, got, 3, "the list must scope to the project")

	first, second := tieA, tieB
	if tieB.ID.String() > tieA.ID.String() {
		first, second = tieB, tieA
	}
	assert.Equal(t, []uuid.UUID{first.ID, second.ID, older.ID}, []uuid.UUID{got[0].ID, got[1].ID, got[2].ID})
	assertSameEndpoint(t, first, got[0])
}

func TestSQLite_List_Empty(t *testing.T) {
	store, _, tc := newSQLiteStore(t)
	got, err := store.List(tenant.Into(t.Context(), tc), tc.OrgID, tc.ProjectID)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSQLite_Delete(t *testing.T) {
	store, _, tc := newSQLiteStore(t)
	ctx := tenant.Into(t.Context(), tc)

	e := newSealedEndpoint(t, tc)
	require.NoError(t, store.Save(ctx, e))
	delivery := uuid.New()
	require.NoError(t, store.SaveAttempt(ctx, newAttempt(tc, e.ID, delivery, 1, time.Now())))

	require.NoError(t, store.Delete(ctx, e.ID))

	_, err := store.ByID(ctx, e.ID)
	assert.True(t, webhook.IsNotFoundError(err))
	attempts, err := store.ListAttempts(ctx, e.ID, delivery)
	require.NoError(t, err)
	assert.Empty(t, attempts, "deleting an endpoint must cascade to its attempts")

	assert.True(t, webhook.IsNotFoundError(store.Delete(ctx, e.ID)), "a second delete reports not found")
}

func TestSQLite_SaveAttemptAndListAttempts(t *testing.T) {
	store, _, tc := newSQLiteStore(t)
	ctx := tenant.Into(t.Context(), tc)

	e := newSealedEndpoint(t, tc)
	other := newSealedEndpoint(t, tc)
	require.NoError(t, store.Save(ctx, e))
	require.NoError(t, store.Save(ctx, other))

	delivery, otherDelivery := uuid.New(), uuid.New()
	base := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	first := newAttempt(tc, e.ID, delivery, 1, base)
	second := noResponse(newAttempt(tc, e.ID, delivery, 2, base.Add(30*time.Second)))
	for _, a := range []webhook.Attempt{
		first, second,
		newAttempt(tc, e.ID, otherDelivery, 1, base),
		newAttempt(tc, other.ID, delivery, 1, base),
	} {
		require.NoError(t, store.SaveAttempt(ctx, a))
	}

	got, err := store.ListAttempts(ctx, e.ID, delivery)
	require.NoError(t, err)
	require.Len(t, got, 2, "attempts must filter by endpoint and delivery")
	assert.Equal(t, second.ID, got[0].ID, "newest first")
	assert.Equal(t, first.ID, got[1].ID)

	a := got[1]
	assert.Equal(t, first.OrgID, a.OrgID)
	assert.Equal(t, first.ProjectID, a.ProjectID)
	assert.Equal(t, first.EndpointID, a.EndpointID)
	assert.Equal(t, first.DeliveryID, a.DeliveryID)
	assert.Equal(t, first.EventID, a.EventID)
	assert.Equal(t, first.EventType, a.EventType)
	assert.Equal(t, 1, a.Attempt)
	assert.Equal(t, 500, a.StatusCode)
	assert.Equal(t, "boom", a.Error)
	assert.Equal(t, 1234*time.Millisecond, a.Duration)
	assert.True(t, first.CreatedAt.Equal(a.CreatedAt))
	assert.Equal(t, time.UTC, a.CreatedAt.Location())
	assert.Equal(t, "dial tcp: connection refused", got[0].Error)
	assertSameResponse(t, first, a)
	assert.Empty(t, got[0].ResponseBody)
	assert.False(t, got[0].ResponseTruncated)
	assert.Empty(t, got[0].ResponseHeaders)
}

func TestSQLite_SaveAttemptFillsIDAndTimeAndTruncatesError(t *testing.T) {
	store, _, tc := newSQLiteStore(t)
	ctx := tenant.Into(t.Context(), tc)
	e := newSealedEndpoint(t, tc)
	require.NoError(t, store.Save(ctx, e))

	delivery := uuid.New()
	a := newAttempt(tc, e.ID, delivery, 1, time.Time{})
	a.ID = uuid.Nil
	a.Error = strings.Repeat("x", outbox.MaxCauseLen-1) + "é"
	require.NoError(t, store.SaveAttempt(ctx, a))

	got, err := store.ListAttempts(ctx, e.ID, delivery)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.NotEqual(t, uuid.Nil, got[0].ID)
	assert.False(t, got[0].CreatedAt.IsZero())
	assert.Equal(t, strings.Repeat("x", outbox.MaxCauseLen-1), got[0].Error, "the cut must not leave a split rune")
}

func TestSQLite_SaveAttemptRefusesAnotherScope(t *testing.T) {
	store, sqlDB, tc := newSQLiteStore(t)
	ctx := tenant.Into(t.Context(), tc)
	e := newSealedEndpoint(t, tc)
	require.NoError(t, store.Save(ctx, e))
	delivery := uuid.New()

	t.Run("another project", func(t *testing.T) {
		a := newAttempt(tc, e.ID, delivery, 1, time.Now())
		a.ProjectID = seedProject(t, sqlDB, tc)
		err := store.SaveAttempt(ctx, a)
		assert.True(t, webhook.IsInvalidAttemptError(err), "got %T: %v", err, err)
	})

	t.Run("another org", func(t *testing.T) {
		b := seedTenant(t, sqlDB)
		a := newAttempt(tc, e.ID, delivery, 1, time.Now())
		a.OrgID = b.OrgID
		err := store.SaveAttempt(ctx, a)
		assert.True(t, webhook.IsInvalidAttemptError(err), "got %T: %v", err, err)
	})

	got, err := store.ListAttempts(ctx, e.ID, delivery)
	require.NoError(t, err)
	assert.Empty(t, got, "a refused attempt must not land")
}

func TestSQLite_SaveAttemptBoundsAndCleansTheResponse(t *testing.T) {
	store, _, tc := newSQLiteStore(t)
	ctx := tenant.Into(t.Context(), tc)
	e := newSealedEndpoint(t, tc)
	require.NoError(t, store.Save(ctx, e))

	manyHeaders := make([]webhook.Header, 0, webhook.MaxResponseHeaders*2)
	for i := range webhook.MaxResponseHeaders * 2 {
		manyHeaders = append(manyHeaders, webhook.Header{Name: "X-Filler-" + strconv.Itoa(i), Value: "v"})
	}
	cases := []struct {
		name          string
		body          string
		headers       []webhook.Header
		wantBody      string
		wantTruncated bool
		wantHeaders   int
	}{
		{name: "NUL and invalid UTF-8", body: "ok\x00\xffend", wantBody: "ok��end"},
		{name: "oversized body", body: strings.Repeat("a", webhook.MaxResponseBodyBytes*2), wantBody: strings.Repeat("a", webhook.MaxResponseBodyBytes), wantTruncated: true},
		{name: "cleaning grows the body past the cap", body: strings.Repeat("\x00", webhook.MaxResponseBodyBytes/2), wantTruncated: true},
		{name: "credential header dropped", headers: []webhook.Header{{Name: "Set-Cookie", Value: "s=1"}, {Name: "X-Ok", Value: "\x00"}}, wantHeaders: 1},
		{name: "header count capped", headers: manyHeaders, wantHeaders: webhook.MaxResponseHeaders},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			delivery := uuid.New()
			a := newAttempt(tc, e.ID, delivery, 1, time.Now())
			a.ResponseBody, a.ResponseTruncated, a.ResponseHeaders = c.body, false, c.headers
			require.NoError(t, store.SaveAttempt(ctx, a))

			got, err := store.ListAttempts(ctx, e.ID, delivery)
			require.NoError(t, err)
			require.Len(t, got, 1)
			r := got[0]
			assert.True(t, utf8.ValidString(r.ResponseBody))
			assert.NotContains(t, r.ResponseBody, "\x00")
			assert.LessOrEqual(t, len(r.ResponseBody), webhook.MaxResponseBodyBytes)
			if c.wantBody != "" {
				assert.Equal(t, c.wantBody, r.ResponseBody)
			}
			assert.Equal(t, c.wantTruncated, r.ResponseTruncated)
			assert.Len(t, r.ResponseHeaders, c.wantHeaders)
			for _, h := range r.ResponseHeaders {
				assert.NotContains(t, h.Value, "\x00")
				assert.NotEqual(t, "Set-Cookie", h.Name)
			}
		})
	}
}
