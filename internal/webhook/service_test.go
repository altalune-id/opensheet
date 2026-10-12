package webhook_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/events"
	"altalune.id/opensheet/internal/platform/outbox"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/webhook"
)

type slugStub struct {
	slug  string
	calls int
}

func (s *slugStub) SlugOf(_ context.Context, _ uuid.UUID) (string, error) {
	s.calls++
	return s.slug, nil
}

type harness struct {
	svc        *webhook.Service
	store      *fakes.WebhookStore
	outbox     *fakes.Outbox
	slugs      *slugStub
	sealer     sealer.Sealer
	unexpected *int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWithSealer(t, newSealer(t))
}

func newHarnessWithSealer(t *testing.T, sl sealer.Sealer) *harness {
	t.Helper()
	calls := 0
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		calls++
		return apperror.New("opensheet.unexpected", err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(err)
	}
	h := &harness{
		store:      fakes.NewWebhookStore(),
		outbox:     fakes.NewOutbox(),
		slugs:      &slugStub{slug: "altalune"},
		sealer:     sl,
		unexpected: &calls,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.svc = webhook.NewService(h.store, log, unexpected, h.sealer, h.outbox, h.slugs)
	return h
}

func svcCtx(t *testing.T) (context.Context, tenant.Context) {
	t.Helper()
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
	return tenant.Into(t.Context(), tc), tc
}

func (h *harness) create(ctx context.Context, t *testing.T, types ...events.Type) (*webhook.Endpoint, string) {
	t.Helper()
	e, secret, err := h.svc.Create(ctx, validURL, "orders", types)
	require.NoError(t, err)
	return e, secret
}

func (h *harness) enqueue(ctx context.Context, typ events.Type, data any) error {
	return fakes.UnitOfWork(ctx, func(ctx context.Context) error {
		return h.svc.Enqueue(ctx, typ, data)
	})
}

func (h *harness) open(t *testing.T, id uuid.UUID, slot string, sealed []byte) string {
	t.Helper()
	got, err := webhook.OpenSecret(h.sealer, id, slot, sealed)
	require.NoError(t, err)
	return got
}

func (h *harness) failEntry(ctx context.Context, t *testing.T, id uuid.UUID) {
	t.Helper()
	for range outbox.MaxAttempts {
		claimed, err := h.outbox.ClaimDue(ctx, time.Now().Add(365*24*time.Hour), outbox.MaxClaimLimit)
		require.NoError(t, err)
		for _, c := range claimed {
			require.NoError(t, h.outbox.Fail(ctx, c, time.Now(), "boom"))
		}
	}
	e, ok := h.outbox.Entry(id)
	require.True(t, ok)
	require.Equal(t, outbox.StatusFailed, e.Status)
}

func postPublished() events.PostPublishedV1 {
	return events.PostPublishedV1{ID: uuid.New(), Slug: "hello", Title: "Hello", TagIDs: []uuid.UUID{}, Version: 1}
}

func decodeEnvelope(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var got map[string]any
	require.NoError(t, json.Unmarshal(payload, &got))
	return got
}

func TestService_Enqueue_RequiresUnitOfWork(t *testing.T) {
	h := newHarness(t)
	ctx, _ := svcCtx(t)
	h.create(ctx, t, events.PostPublished)

	err := h.svc.Enqueue(ctx, events.PostPublished, postPublished())
	assert.True(t, webhook.IsNoUnitOfWorkError(err), "got %T: %v", err, err)
	assert.Empty(t, h.outbox.Entries())
}

func TestService_Enqueue_RejectsBadEvents(t *testing.T) {
	h := newHarness(t)
	ctx, _ := svcCtx(t)
	h.create(ctx, t, events.PostPublished)

	err := h.enqueue(ctx, events.Type("blog.post.nope"), postPublished())
	assert.True(t, events.IsUnknownTypeError(err), "got %T: %v", err, err)

	err = h.enqueue(ctx, events.PostPublished, events.PostDeletedV1{ID: uuid.New()})
	assert.True(t, events.IsPayloadMismatchError(err), "got %T: %v", err, err)

	assert.Empty(t, h.outbox.Entries())
	assert.Zero(t, *h.unexpected)
}

func TestService_Enqueue_FansOutToActiveSubscribers(t *testing.T) {
	h := newHarness(t)
	ctx, tc := svcCtx(t)
	a, _ := h.create(ctx, t, events.PostPublished)
	b, _ := h.create(ctx, t, events.PostPublished, events.PostDeleted)
	h.create(ctx, t, events.PostDeleted)
	inactive, _ := h.create(ctx, t, events.PostPublished)
	_, err := h.svc.Update(ctx, inactive.ID, inactive.URL, inactive.Description, inactive.EventTypes, false)
	require.NoError(t, err)

	otherProject := tenant.Context{OrgID: tc.OrgID, ProjectID: uuid.New()}
	h.create(tenant.Into(t.Context(), otherProject), t, events.PostPublished)

	data := postPublished()
	require.NoError(t, h.enqueue(ctx, events.PostPublished, data))

	entries := h.outbox.Entries()
	require.Len(t, entries, 2)
	assert.ElementsMatch(t, []string{a.ID.String(), b.ID.String()}, []string{entries[0].Target, entries[1].Target})
	assert.Equal(t, entries[0].EventID, entries[1].EventID)
	assert.NotEqual(t, entries[0].ID, entries[1].ID)
	assert.Equal(t, entries[0].Payload, entries[1].Payload, "one event is one body")
	assert.Equal(t, 1, h.slugs.calls)
	assert.Equal(t, uuid.Version(7), entries[0].EventID.Version())
	assert.Equal(t, uuid.Version(7), entries[0].ID.Version())

	for _, e := range entries {
		assert.Equal(t, tc.OrgID, e.OrgID)
		assert.Equal(t, tc.ProjectID, e.ProjectID)
	}

	env := decodeEnvelope(t, entries[0].Payload)
	assert.Equal(t, webhook.EventIDPrefix+entries[0].EventID.String(), env["id"])
	assert.Equal(t, "blog.post.published", env["type"])
	assert.Equal(t, "v1", env["api_version"])
	assert.Regexp(t, `^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`, env["created_at"], "created_at is UTC whole seconds")
	createdAt, err := time.Parse(time.RFC3339, env["created_at"].(string))
	require.NoError(t, err)
	assert.Zero(t, createdAt.Nanosecond())
	assert.WithinDuration(t, time.Now(), createdAt, time.Minute)
	assert.Equal(t, map[string]any{
		"org_id":       tc.OrgID.String(),
		"project_id":   tc.ProjectID.String(),
		"project_slug": "altalune",
	}, env["tenant"])
	assert.Equal(t, data.ID.String(), env["data"].(map[string]any)["id"])
	assert.Zero(t, *h.unexpected)
}

func TestService_Enqueue_NoSubscriberWritesNothing(t *testing.T) {
	h := newHarness(t)
	ctx, _ := svcCtx(t)
	h.create(ctx, t, events.PostDeleted)

	require.NoError(t, h.enqueue(ctx, events.PostPublished, postPublished()))
	assert.Empty(t, h.outbox.Entries())
	assert.Zero(t, h.slugs.calls, "no subscriber means no slug lookup")
}

func TestService_Enqueue_WrapsStoreErrorsWithoutReporting(t *testing.T) {
	h := newHarness(t)
	ctx, _ := svcCtx(t)
	h.create(ctx, t, events.PostPublished)
	boom := errors.New("boom")
	h.outbox.EnqueueErr = boom

	err := h.enqueue(ctx, events.PostPublished, postPublished())
	require.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "webhook.Enqueue")
	assert.Zero(t, *h.unexpected, "the calling service reports once")
}

func TestService_Create(t *testing.T) {
	t.Run("returns the secret once and stores it sealed", func(t *testing.T) {
		h := newHarness(t)
		ctx, tc := svcCtx(t)

		e, secret, err := h.svc.Create(ctx, validURL, "orders", []events.Type{events.PostPublished})
		require.NoError(t, err)
		assert.Equal(t, tc.OrgID, e.OrgID)
		assert.Equal(t, tc.ProjectID, e.ProjectID)
		assert.True(t, e.Active)

		stored, err := h.store.ByID(ctx, e.ID)
		require.NoError(t, err)
		assert.NotContains(t, string(stored.Secrets.Primary), secret)
		assert.Equal(t, secret, h.open(t, e.ID, webhook.SlotPrimary, stored.Secrets.Primary))
		assert.Nil(t, stored.Secrets.Secondary)
	})

	t.Run("invalid input bubbles the typed error", func(t *testing.T) {
		h := newHarness(t)
		ctx, _ := svcCtx(t)

		_, _, err := h.svc.Create(ctx, "http://example.com", "", []events.Type{events.PostPublished})
		assert.True(t, webhook.IsInvalidURLError(err), "got %T: %v", err, err)
		assert.Zero(t, *h.unexpected)
	})

	t.Run("the eleventh endpoint is refused", func(t *testing.T) {
		h := newHarness(t)
		ctx, _ := svcCtx(t)
		for range webhook.MaxEndpointsPerProject {
			h.create(ctx, t, events.PostPublished)
		}

		_, _, err := h.svc.Create(ctx, validURL, "", []events.Type{events.PostPublished})
		assert.True(t, webhook.IsEndpointLimitError(err), "got %T: %v", err, err)
		assert.Zero(t, *h.unexpected)
	})

	t.Run("a disabled sealer is reported as unavailable", func(t *testing.T) {
		h := newHarnessWithSealer(t, sealer.Disabled())
		ctx, _ := svcCtx(t)

		_, _, err := h.svc.Create(ctx, validURL, "", []events.Type{events.PostPublished})
		assert.True(t, sealer.IsUnavailableError(err), "got %T: %v", err, err)
		assert.Zero(t, *h.unexpected)
	})
}

func TestService_UpdateAndDelete(t *testing.T) {
	h := newHarness(t)
	ctx, _ := svcCtx(t)
	e, _ := h.create(ctx, t, events.PostPublished)

	got, err := h.svc.Update(ctx, e.ID, "https://example.org/v2", "renamed", []events.Type{events.PostDeleted}, false)
	require.NoError(t, err)
	assert.Equal(t, "https://example.org/v2", got.URL)
	assert.False(t, got.Active)
	assert.Equal(t, e.Secrets, got.Secrets, "update leaves the secrets alone")

	_, err = h.svc.Update(ctx, e.ID, validURL, "", nil, true)
	assert.True(t, webhook.IsInvalidEventTypesError(err), "got %T: %v", err, err)

	require.NoError(t, h.svc.Delete(ctx, e.ID))
	_, err = h.svc.ByID(ctx, e.ID)
	assert.True(t, webhook.IsNotFoundError(err), "got %T: %v", err, err)
	assert.Zero(t, *h.unexpected)
}

func TestService_List(t *testing.T) {
	h := newHarness(t)
	ctx, tc := svcCtx(t)
	a, _ := h.create(ctx, t, events.PostPublished)
	h.create(tenant.Into(t.Context(), tenant.Context{OrgID: tc.OrgID, ProjectID: uuid.New()}), t, events.PostPublished)

	got, err := h.svc.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, a.ID, got[0].ID)
}

func TestService_Rotate(t *testing.T) {
	t.Run("the old primary becomes the secondary", func(t *testing.T) {
		h := newHarness(t)
		ctx, _ := svcCtx(t)
		e, old := h.create(ctx, t, events.PostPublished)

		fresh, err := h.svc.Rotate(ctx, e.ID)
		require.NoError(t, err)
		assert.NotEqual(t, old, fresh)

		stored, err := h.store.ByID(ctx, e.ID)
		require.NoError(t, err)
		assert.Equal(t, fresh, h.open(t, e.ID, webhook.SlotPrimary, stored.Secrets.Primary))
		assert.Equal(t, old, h.open(t, e.ID, webhook.SlotSecondary, stored.Secrets.Secondary))
		assert.NotEqual(t, e.Secrets.Primary, stored.Secrets.Secondary, "sealed bytes never move between slots")
	})

	t.Run("an unopenable primary leaves no secondary", func(t *testing.T) {
		h := newHarness(t)
		ctx, _ := svcCtx(t)
		e, _ := h.create(ctx, t, events.PostPublished)
		foreign, err := webhook.SealSecret(newSealer(t), e.ID, webhook.SlotPrimary, "whsec_lost")
		require.NoError(t, err)
		e.Secrets = webhook.SealedSecrets{Primary: foreign}
		h.store.Seed(e)

		fresh, err := h.svc.Rotate(ctx, e.ID)
		require.NoError(t, err)

		stored, err := h.store.ByID(ctx, e.ID)
		require.NoError(t, err)
		assert.Equal(t, fresh, h.open(t, e.ID, webhook.SlotPrimary, stored.Secrets.Primary))
		assert.Nil(t, stored.Secrets.Secondary)
		assert.Zero(t, *h.unexpected)
	})
}

func TestService_RetireSecondary(t *testing.T) {
	h := newHarness(t)
	ctx, _ := svcCtx(t)
	e, _ := h.create(ctx, t, events.PostPublished)
	fresh, err := h.svc.Rotate(ctx, e.ID)
	require.NoError(t, err)

	require.NoError(t, h.svc.RetireSecondary(ctx, e.ID))

	stored, err := h.store.ByID(ctx, e.ID)
	require.NoError(t, err)
	assert.Nil(t, stored.Secrets.Secondary)
	assert.Equal(t, fresh, h.open(t, e.ID, webhook.SlotPrimary, stored.Secrets.Primary))
}

func TestService_UpdateRacingARotateKeepsTheNewSecret(t *testing.T) {
	h := newHarness(t)
	ctx, _ := svcCtx(t)
	e, _ := h.create(ctx, t, events.PostPublished)
	var fresh string
	h.store.AfterByID = func() {
		h.store.AfterByID = nil
		var err error
		fresh, err = h.svc.Rotate(ctx, e.ID)
		require.NoError(t, err)
	}

	_, err := h.svc.Update(ctx, e.ID, "https://example.org/v2", "renamed", []events.Type{events.PostDeleted}, true)
	require.NoError(t, err)

	stored, err := h.store.ByID(ctx, e.ID)
	require.NoError(t, err)
	assert.Equal(t, fresh, h.open(t, e.ID, webhook.SlotPrimary, stored.Secrets.Primary), "the update must not revert the rotation")
	assert.Equal(t, "https://example.org/v2", stored.URL)
}

func TestService_SecretWritesRacingARotateConflict(t *testing.T) {
	calls := []struct {
		name string
		call func(svc *webhook.Service, ctx context.Context, id uuid.UUID) error
	}{
		{"Rotate", func(svc *webhook.Service, ctx context.Context, id uuid.UUID) error {
			_, err := svc.Rotate(ctx, id)
			return err
		}},
		{"RetireSecondary", func(svc *webhook.Service, ctx context.Context, id uuid.UUID) error {
			return svc.RetireSecondary(ctx, id)
		}},
	}
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			ctx, _ := svcCtx(t)
			e, _ := h.create(ctx, t, events.PostPublished)
			_, err := h.svc.Rotate(ctx, e.ID)
			require.NoError(t, err)
			var fresh string
			h.store.AfterByID = func() {
				h.store.AfterByID = nil
				var err error
				fresh, err = h.svc.Rotate(ctx, e.ID)
				require.NoError(t, err)
			}

			err = c.call(h.svc, ctx, e.ID)
			assert.True(t, webhook.IsSecretConflictError(err), "got %T: %v", err, err)
			assert.Zero(t, *h.unexpected)

			stored, err := h.store.ByID(ctx, e.ID)
			require.NoError(t, err)
			assert.Equal(t, fresh, h.open(t, e.ID, webhook.SlotPrimary, stored.Secrets.Primary), "the racing rotation must survive")
		})
	}
}

func TestService_SendTest(t *testing.T) {
	t.Run("inactive is refused", func(t *testing.T) {
		h := newHarness(t)
		ctx, _ := svcCtx(t)
		e, _ := h.create(ctx, t, events.PostPublished)
		_, err := h.svc.Update(ctx, e.ID, e.URL, e.Description, e.EventTypes, false)
		require.NoError(t, err)

		err = h.svc.SendTest(ctx, e.ID)
		assert.True(t, webhook.IsEndpointInactiveError(err), "got %T: %v", err, err)
		assert.Empty(t, h.outbox.Entries())
	})

	t.Run("one ping to that endpoint, ignoring subscriptions, without a caller tx", func(t *testing.T) {
		h := newHarness(t)
		ctx, tc := svcCtx(t)
		e, _ := h.create(ctx, t, events.PostDeleted)
		h.create(ctx, t, events.PostDeleted)

		require.NoError(t, h.svc.SendTest(ctx, e.ID))

		entries := h.outbox.Entries()
		require.Len(t, entries, 1)
		assert.Equal(t, e.ID.String(), entries[0].Target)
		assert.Equal(t, tc.ProjectID, entries[0].ProjectID)
		env := decodeEnvelope(t, entries[0].Payload)
		assert.Equal(t, "webhook.ping", env["type"])
		assert.Equal(t, webhook.EventIDPrefix+entries[0].EventID.String(), env["id"])
		assert.Equal(t, map[string]any{"endpoint_id": e.ID.String()}, env["data"])
	})
}

func TestService_Deliveries(t *testing.T) {
	h := newHarness(t)
	ctx, _ := svcCtx(t)
	e, _ := h.create(ctx, t, events.PostPublished)
	other, _ := h.create(ctx, t, events.PostPublished)
	for range webhook.MaxDeliveriesListed + 1 {
		require.NoError(t, h.svc.SendTest(ctx, e.ID))
	}
	require.NoError(t, h.svc.SendTest(ctx, other.ID))

	got, err := h.svc.Deliveries(ctx, e.ID, 1000)
	require.NoError(t, err)
	assert.Len(t, got, webhook.MaxDeliveriesListed)
	for _, d := range got {
		assert.Equal(t, e.ID.String(), d.Target)
		assert.Equal(t, events.WebhookPing, d.EventType)
	}

	got, err = h.svc.Deliveries(ctx, e.ID, 3)
	require.NoError(t, err)
	assert.Len(t, got, 3)

	for _, limit := range []int{0, -5} {
		got, err = h.svc.Deliveries(ctx, e.ID, limit)
		require.NoError(t, err)
		assert.Len(t, got, 1, "limit %d clamps up to 1", limit)
	}
}

func TestService_DeliveriesListsUndecodableEnvelopesWithoutType(t *testing.T) {
	h := newHarness(t)
	ctx, tc := svcCtx(t)
	e, _ := h.create(ctx, t, events.PostPublished)
	for _, payload := range []string{`not json`, `{"type":""}`} {
		require.NoError(t, h.outbox.Enqueue(ctx, outbox.Entry{
			ID:        uuid.Must(uuid.NewV7()),
			EventID:   uuid.Must(uuid.NewV7()),
			ProjectID: tc.ProjectID,
			Target:    e.ID.String(),
			Payload:   []byte(payload),
		}))
	}

	got, err := h.svc.Deliveries(ctx, e.ID, 10)

	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, d := range got {
		assert.Empty(t, d.EventType)
	}
	assert.Zero(t, *h.unexpected)
}

func TestService_Attempts(t *testing.T) {
	h := newHarness(t)
	ctx, tc := svcCtx(t)
	e, _ := h.create(ctx, t, events.PostPublished)
	deliveryID := uuid.New()
	a := newAttempt(tc, e.ID, deliveryID, 1, time.Now())
	require.NoError(t, h.store.SaveAttempt(ctx, a))
	require.NoError(t, h.store.SaveAttempt(ctx, newAttempt(tc, e.ID, uuid.New(), 1, time.Now())))

	got, err := h.svc.Attempts(ctx, e.ID, deliveryID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, a.ID, got[0].ID)
}

func TestService_Delivery(t *testing.T) {
	h := newHarness(t)
	ctx, _ := svcCtx(t)
	e, _ := h.create(ctx, t, events.PostPublished)
	other, _ := h.create(ctx, t, events.PostPublished)
	require.NoError(t, h.svc.SendTest(ctx, e.ID))
	d := h.outbox.Entries()[0]

	got, err := h.svc.Delivery(ctx, e.ID, d.ID)
	require.NoError(t, err)
	assert.Equal(t, d.ID, got.ID)
	assert.Equal(t, d.EventID, got.EventID)
	assert.Equal(t, d.Payload, got.Payload)
	assert.Equal(t, events.WebhookPing, got.EventType)

	_, err = h.svc.Delivery(ctx, other.ID, d.ID)
	assert.True(t, webhook.IsDeliveryNotFoundError(err), "another endpoint's delivery: got %T: %v", err, err)

	_, err = h.svc.Delivery(ctx, e.ID, uuid.New())
	assert.True(t, webhook.IsDeliveryNotFoundError(err), "an unknown delivery: got %T: %v", err, err)
	assert.Zero(t, *h.unexpected)
}

func TestService_DeliveryReadsAnUndecodableEnvelopeWithoutType(t *testing.T) {
	h := newHarness(t)
	ctx, tc := svcCtx(t)
	e, _ := h.create(ctx, t, events.PostPublished)
	entry := outbox.Entry{
		ID:        uuid.Must(uuid.NewV7()),
		EventID:   uuid.Must(uuid.NewV7()),
		ProjectID: tc.ProjectID,
		Target:    e.ID.String(),
		Payload:   []byte(`not json`),
	}
	require.NoError(t, h.outbox.Enqueue(ctx, entry))

	got, err := h.svc.Delivery(ctx, e.ID, entry.ID)

	require.NoError(t, err)
	assert.Equal(t, []byte(`not json`), got.Payload)
	assert.Empty(t, got.EventType)
	assert.Zero(t, *h.unexpected)
}

func TestService_Retry(t *testing.T) {
	h := newHarness(t)
	ctx, _ := svcCtx(t)
	e, _ := h.create(ctx, t, events.PostPublished)
	other, _ := h.create(ctx, t, events.PostPublished)
	require.NoError(t, h.svc.SendTest(ctx, e.ID))
	d := h.outbox.Entries()[0]

	err := h.svc.Retry(ctx, e.ID, d.ID)
	assert.True(t, webhook.IsDeliveryNotRetryableError(err), "a pending delivery: got %T: %v", err, err)

	err = h.svc.Retry(ctx, e.ID, uuid.New())
	assert.True(t, webhook.IsDeliveryNotFoundError(err), "an unknown delivery: got %T: %v", err, err)

	h.failEntry(ctx, t, d.ID)
	err = h.svc.Retry(ctx, other.ID, d.ID)
	assert.True(t, webhook.IsDeliveryNotFoundError(err), "another endpoint's delivery: got %T: %v", err, err)

	require.NoError(t, h.svc.Retry(ctx, e.ID, d.ID))
	got, ok := h.outbox.Entry(d.ID)
	require.True(t, ok)
	assert.Equal(t, outbox.StatusPending, got.Status)
	assert.Zero(t, got.Attempt)
	assert.Zero(t, *h.unexpected)
}

func TestService_RetryFailed(t *testing.T) {
	h := newHarness(t)
	ctx, _ := svcCtx(t)
	e, _ := h.create(ctx, t, events.PostPublished)
	require.NoError(t, h.svc.SendTest(ctx, e.ID))
	require.NoError(t, h.svc.SendTest(ctx, e.ID))
	h.failEntry(ctx, t, h.outbox.Entries()[0].ID)

	n, err := h.svc.RetryFailed(ctx, e.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}

func TestService_BareIDMethodsCheckOrgAndProject(t *testing.T) {
	methods := []struct {
		name string
		call func(svc *webhook.Service, ctx context.Context, id uuid.UUID) error
	}{
		{"ByID", func(svc *webhook.Service, ctx context.Context, id uuid.UUID) error {
			_, err := svc.ByID(ctx, id)
			return err
		}},
		{"Update", func(svc *webhook.Service, ctx context.Context, id uuid.UUID) error {
			_, err := svc.Update(ctx, id, validURL, "hijack", validTypes(), true)
			return err
		}},
		{"Delete", func(svc *webhook.Service, ctx context.Context, id uuid.UUID) error {
			return svc.Delete(ctx, id)
		}},
		{"Rotate", func(svc *webhook.Service, ctx context.Context, id uuid.UUID) error {
			_, err := svc.Rotate(ctx, id)
			return err
		}},
		{"RetireSecondary", func(svc *webhook.Service, ctx context.Context, id uuid.UUID) error {
			return svc.RetireSecondary(ctx, id)
		}},
		{"SendTest", func(svc *webhook.Service, ctx context.Context, id uuid.UUID) error {
			return svc.SendTest(ctx, id)
		}},
		{"Deliveries", func(svc *webhook.Service, ctx context.Context, id uuid.UUID) error {
			_, err := svc.Deliveries(ctx, id, 10)
			return err
		}},
		{"Attempts", func(svc *webhook.Service, ctx context.Context, id uuid.UUID) error {
			_, err := svc.Attempts(ctx, id, uuid.New())
			return err
		}},
		{"Delivery", func(svc *webhook.Service, ctx context.Context, id uuid.UUID) error {
			_, err := svc.Delivery(ctx, id, uuid.New())
			return err
		}},
		{"Retry", func(svc *webhook.Service, ctx context.Context, id uuid.UUID) error {
			return svc.Retry(ctx, id, uuid.New())
		}},
		{"RetryFailed", func(svc *webhook.Service, ctx context.Context, id uuid.UUID) error {
			_, err := svc.RetryFailed(ctx, id)
			return err
		}},
	}
	scopes := []struct {
		name  string
		other func(tc tenant.Context) tenant.Context
	}{
		{"sibling project", func(tc tenant.Context) tenant.Context {
			return tenant.Context{OrgID: tc.OrgID, ProjectID: uuid.New(), UserID: tc.UserID}
		}},
		{"other org", func(tc tenant.Context) tenant.Context {
			return tenant.Context{OrgID: uuid.New(), ProjectID: tc.ProjectID, UserID: tc.UserID}
		}},
	}
	for _, sc := range scopes {
		for _, m := range methods {
			t.Run(sc.name+"/"+m.name, func(t *testing.T) {
				h := newHarness(t)
				ctx, tc := svcCtx(t)
				e, _ := h.create(ctx, t, events.PostPublished)
				before, err := h.store.ByID(ctx, e.ID)
				require.NoError(t, err)

				err = m.call(h.svc, tenant.Into(t.Context(), sc.other(tc)), e.ID)
				assert.True(t, webhook.IsNotFoundError(err), "got %T: %v", err, err)

				after, err := h.store.ByID(ctx, e.ID)
				require.NoError(t, err)
				assert.Equal(t, before, after, "the endpoint must be untouched")
				assert.Empty(t, h.outbox.Entries())
				assert.Zero(t, *h.unexpected)
			})
		}
	}
}

func TestEnvelope_GoldenV1(t *testing.T) {
	first := time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC)
	env := webhook.Envelope{
		ID:         webhook.EventIDPrefix + "0199c1f0-0000-7000-8000-000000000001",
		Type:       events.PostPublished,
		APIVersion: "v1",
		CreatedAt:  time.Date(2026, 9, 27, 4, 11, 0, 0, time.UTC),
		Tenant: webhook.EnvelopeTenant{
			OrgID:       uuid.MustParse("0199c1f0-0000-7000-8000-00000000000a"),
			ProjectID:   uuid.MustParse("0199c1f0-0000-7000-8000-00000000000b"),
			ProjectSlug: "altalune",
		},
		Data: events.PostPublishedV1{
			ID:               uuid.MustParse("0199c1f0-0000-7000-8000-00000000000c"),
			Slug:             "hello-world",
			Title:            "Hello, world",
			BodyMarkdown:     "# Hello",
			CategoryID:       uuid.MustParse("0199c1f0-0000-7000-8000-00000000000d"),
			TagIDs:           []uuid.UUID{uuid.MustParse("0199c1f0-0000-7000-8000-00000000000e")},
			FirstPublishedAt: &first,
			UpdatedAt:        time.Date(2026, 9, 27, 4, 10, 0, 0, time.UTC),
			Version:          3,
		},
	}
	got, err := json.MarshalIndent(env, "", "  ")
	require.NoError(t, err)

	want, err := os.ReadFile(filepath.Join("testdata", "envelope_v1.golden.json"))
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(got))
	assert.Equal(t, string(want), string(got)+"\n", "field order is part of the contract")
}
