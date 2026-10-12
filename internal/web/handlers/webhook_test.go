package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/i18n"
	"altalune.id/opensheet/internal/platform/events"
	"altalune.id/opensheet/internal/platform/outbox"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/handlers"
	"altalune.id/opensheet/internal/web/templates"
	"altalune.id/opensheet/internal/webhook"
)

const webhookBase = "/orgs/acme/projects/alpha/webhooks"

type slugOf struct{}

func (slugOf) SlugOf(context.Context, uuid.UUID) (string, error) { return "alpha", nil }

type webhookFixture struct {
	*handlerFixture
	Hooks  *webhook.Service
	Store  *fakes.WebhookStore
	Sealer sealer.Sealer
	Outbox *fakes.Outbox
	Mux    *http.ServeMux

	uid     uuid.UUID
	org     uuid.UUID
	project *project.Project
}

func newWebhookFixture(t *testing.T, ephemeral bool) *webhookFixture {
	t.Helper()
	f := newFixture(t)
	f.Deps.Caps.EphemeralEncryptionKey = ephemeral

	key, err := sealer.GenerateKey()
	require.NoError(t, err)
	sl, err := sealer.New(key)
	require.NoError(t, err)
	ob := fakes.NewOutbox()
	store := fakes.NewWebhookStore()
	hooks := webhook.NewService(store, discardLogger(), passthroughUnexpected(), sl, ob, slugOf{})

	uid := uuid.New()
	o := f.seedOrg(t, "acme", uid)
	proj, err := f.Projects.Create(setTenant(context.Background(), o.ID, uid), o.ID, "alpha", "Alpha")
	require.NoError(t, err)

	mux := http.NewServeMux()
	handlers.NewWebhookHandler(f.Deps, f.Projects, hooks).Register(mux)
	return &webhookFixture{handlerFixture: f, Hooks: hooks, Store: store, Sealer: sl, Outbox: ob, Mux: mux, uid: uid, org: o.ID, project: proj}
}

func (x *webhookFixture) do(t *testing.T, method, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return x.doAs(t, method, target, form, session.Principal{UserID: x.uid, ActiveOrgID: x.org})
}

func (x *webhookFixture) doAs(t *testing.T, method, target string, form url.Values, p session.Principal) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, x.authedRequest(t, method, target, form.Encode(), p))
	return rec
}

func (x *webhookFixture) doHX(t *testing.T, method, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := x.authedRequest(t, method, target, form.Encode(), session.Principal{UserID: x.uid, ActiveOrgID: x.org})
	r.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, r)
	return rec
}

func (x *webhookFixture) ctx() context.Context {
	return setTenantProject(context.Background(), x.org, x.project.ID, x.uid)
}

func (x *webhookFixture) create(t *testing.T) *webhook.Endpoint {
	t.Helper()
	e, _, err := x.Hooks.Create(x.ctx(), "https://example.com/hook", "orders", []events.Type{events.PostPublished})
	require.NoError(t, err)
	return e
}

func (x *webhookFixture) settle(t *testing.T, fail bool) {
	t.Helper()
	for range outbox.MaxAttempts {
		claimed, err := x.Outbox.ClaimDue(x.ctx(), time.Now().Add(365*24*time.Hour), outbox.MaxClaimLimit)
		require.NoError(t, err)
		for _, c := range claimed {
			if fail {
				require.NoError(t, x.Outbox.Fail(x.ctx(), c, time.Now(), "dial tcp: connection refused"))
				continue
			}
			require.NoError(t, x.Outbox.Succeed(x.ctx(), c, time.Now()))
		}
	}
}

func endpointForm(rawURL string, types ...string) url.Values {
	return url.Values{"url": {rawURL}, "description": {"orders"}, "event_types": types}
}

var reSecret = regexp.MustCompile(`whsec_[A-Za-z0-9_-]{20,}`)

// TestWebhookHandler_CreateShowsTheSecretOnce pins the one-time reveal: the create response carries it, a later GET never does.
func TestWebhookHandler_CreateShowsTheSecretOnce(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)

	rec := x.do(t, http.MethodPost, webhookBase, endpointForm("https://example.com/hook", string(events.PostPublished)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	secret := reSecret.FindString(rec.Body.String())
	require.NotEmpty(t, secret, "the create response must reveal the new secret")
	assert.Contains(t, rec.Body.String(), "data-webhook-secret")
	assert.Contains(t, rec.Body.String(), "data-copy=", "the reveal carries the copy button")

	items, err := x.Hooks.List(x.ctx())
	require.NoError(t, err)
	require.Len(t, items, 1)

	rec = x.do(t, http.MethodGet, webhookBase+"/"+items[0].ID.String(), nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), secret, "a later GET must never contain the secret")
	assert.NotContains(t, rec.Body.String(), "data-webhook-secret")
	assert.Contains(t, rec.Body.String(), templates.WebhookSecretMask)
}

// TestWebhookHandler_RotateShowsTheNewSecretOnceAndTheRotationState covers rotate, the secondary state and retire.
func TestWebhookHandler_RotateShowsTheNewSecretOnceAndTheRotationState(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	e := x.create(t)
	detail := webhookBase + "/" + e.ID.String()

	rec := x.do(t, http.MethodPost, detail+"/rotate", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	secret := reSecret.FindString(rec.Body.String())
	require.NotEmpty(t, secret)
	assert.Contains(t, rec.Body.String(), "webhooks.rotating")
	assert.Contains(t, rec.Body.String(), detail+"/retire")

	rec = x.do(t, http.MethodGet, detail, nil)
	assert.NotContains(t, rec.Body.String(), secret)

	rec = x.do(t, http.MethodPost, detail+"/retire", nil)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, detail, rec.Header().Get("Location"))
	got, err := x.Hooks.ByID(x.ctx(), e.ID)
	require.NoError(t, err)
	assert.Nil(t, got.Secrets.Secondary)
}

// TestWebhookHandler_EphemeralKeyBanner renders the restart warning only when boot reports the key ephemeral.
func TestWebhookHandler_EphemeralKeyBanner(t *testing.T) {
	t.Parallel()
	for _, ephemeral := range []bool{true, false} {
		x := newWebhookFixture(t, ephemeral)
		e := x.create(t)
		for _, path := range []string{webhookBase, webhookBase + "/new", webhookBase + "/" + e.ID.String()} {
			rec := x.do(t, http.MethodGet, path, nil)
			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, ephemeral, strings.Contains(rec.Body.String(), "data-ephemeral-key"), "%s ephemeral=%v", path, ephemeral)
		}
	}
}

// TestWebhookHandler_EventPickerListsOnlySubscribableTypes keeps webhook.ping out of the subscription form.
func TestWebhookHandler_EventPickerListsOnlySubscribableTypes(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)

	rec := x.do(t, http.MethodGet, webhookBase+"/new", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	for _, s := range events.All() {
		want := `name="event_types" value="` + string(s.Type) + `"`
		assert.Equal(t, s.Subscribable, strings.Contains(body, want), "%s subscribable=%v", s.Type, s.Subscribable)
	}
	assert.Contains(t, body, `maxlength="2048"`)
	assert.Contains(t, body, `maxlength="200"`)
}

// TestWebhookHandler_CreateErrorsRenderTheirCode shows a WHK code in the form banner and keeps the input.
func TestWebhookHandler_CreateErrorsRenderTheirCode(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)

	rec := x.do(t, http.MethodPost, webhookBase, endpointForm("http://example.com/hook", string(events.PostPublished)))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "webhooks.error.invalid_url")
	assert.Contains(t, rec.Body.String(), apperror.CodeWebhookInvalidURL)
	assert.Contains(t, rec.Body.String(), `value="http://example.com/hook"`)

	rec = x.do(t, http.MethodPost, webhookBase, endpointForm("https://example.com/hook", "webhook.ping"))
	assert.Contains(t, rec.Body.String(), apperror.CodeWebhookInvalidEventTypes, "an unsubscribable type is dropped, leaving none")
	assert.NotContains(t, rec.Body.String(), "data-webhook-secret")
}

// TestWebhookHandler_UpdateAndDelete covers the edit round trip and the delete redirect.
func TestWebhookHandler_UpdateAndDelete(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	e := x.create(t)
	detail := webhookBase + "/" + e.ID.String()

	form := endpointForm("https://example.com/other", string(events.PostDeleted), string(events.PostPublished))
	rec := x.do(t, http.MethodPost, detail, form)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	got, err := x.Hooks.ByID(x.ctx(), e.ID)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/other", got.URL)
	assert.Equal(t, []events.Type{events.PostPublished, events.PostDeleted}, got.EventTypes)
	assert.False(t, got.Active, "an unchecked active box deactivates")

	rec = x.do(t, http.MethodPost, detail+"/delete", nil)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, webhookBase, rec.Header().Get("Location"))
	_, err = x.Hooks.ByID(x.ctx(), e.ID)
	require.True(t, webhook.IsNotFoundError(err))
}

// TestWebhookHandler_DeliveriesTestRetryAndAttempts drives send test, the delivery row, retry one and retry all.
func TestWebhookHandler_DeliveriesTestRetryAndAttempts(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	e := x.create(t)
	detail := webhookBase + "/" + e.ID.String()

	rec := x.do(t, http.MethodPost, detail+"/test", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `id="webhook-deliveries"`)
	assert.Contains(t, rec.Body.String(), "webhooks.test_queued")
	assert.Contains(t, rec.Body.String(), "webhooks.event.webhook_ping")
	assert.Contains(t, rec.Body.String(), "webhooks.status.pending")

	x.settle(t, true)
	entries := x.Outbox.Entries()
	require.Len(t, entries, 1)
	did := entries[0].ID.String()

	rec = x.do(t, http.MethodGet, detail, nil)
	body := rec.Body.String()
	assert.Contains(t, body, "webhooks.status.failed")
	assert.Contains(t, body, "8/8", "attempt n/MaxAttempts")
	assert.Contains(t, body, "dial tcp: connection refused")
	assert.Contains(t, body, "webhooks.retry_all")
	assert.Contains(t, body, detail+"/deliveries/"+did+"/retry")

	rec = x.do(t, http.MethodGet, detail+"/deliveries/"+did, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "webhooks.attempts_empty")

	rec = x.do(t, http.MethodPost, detail+"/deliveries/"+did+"/retry", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `id="delivery-`+did+`"`)
	assert.Contains(t, rec.Body.String(), "webhooks.status.pending")
	assert.Contains(t, rec.Body.String(), `hx-target="#webhook-retry-all"`)

	x.settle(t, true)
	rec = x.do(t, http.MethodPost, detail+"/retry-failed", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "webhooks.retried")
	got, ok := x.Outbox.Entry(entries[0].ID)
	require.True(t, ok)
	assert.Equal(t, outbox.StatusPending, got.Status)
}

// TestWebhookHandler_RetryOnADeliveredRowShowsWHK005 renders the not-retryable code in the row fragment.
func TestWebhookHandler_RetryOnADeliveredRowShowsWHK005(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	e := x.create(t)
	require.NoError(t, x.Hooks.SendTest(x.ctx(), e.ID))
	x.settle(t, false)
	did := x.Outbox.Entries()[0].ID.String()

	rec := x.do(t, http.MethodPost, webhookBase+"/"+e.ID.String()+"/deliveries/"+did+"/retry", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "webhooks.error.not_retryable")
	assert.Contains(t, rec.Body.String(), apperror.CodeWebhookDeliveryNotRetryable)
	assert.Equal(t, "WHK005", apperror.CodeWebhookDeliveryNotRetryable)
}

func TestWebhookHandler_RotateRacingARotateShowsWHK008(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	e := x.create(t)
	loads := 0
	x.Store.AfterByID = func() {
		loads++
		if loads == 2 {
			_, err := x.Hooks.Rotate(x.ctx(), e.ID)
			require.NoError(t, err)
		}
	}

	rec := x.do(t, http.MethodPost, webhookBase+"/"+e.ID.String()+"/rotate", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "webhooks.error.secret_conflict")
	assert.Contains(t, rec.Body.String(), apperror.CodeWebhookSecretConflict)
	assert.Equal(t, "WHK008", apperror.CodeWebhookSecretConflict)
	assert.Empty(t, reSecret.FindString(rec.Body.String()), "a refused rotation must not reveal a secret")
}

func webhookRoutes(id, did string) []struct{ method, path string } {
	d := webhookBase + "/" + id
	return []struct{ method, path string }{
		{http.MethodGet, webhookBase},
		{http.MethodGet, webhookBase + "/new"},
		{http.MethodPost, webhookBase},
		{http.MethodGet, d},
		{http.MethodPost, d},
		{http.MethodPost, d + "/delete"},
		{http.MethodPost, d + "/rotate"},
		{http.MethodPost, d + "/retire"},
		{http.MethodPost, d + "/test"},
		{http.MethodGet, d + "/deliveries/" + did},
		{http.MethodPost, d + "/deliveries/" + did + "/retry"},
		{http.MethodPost, d + "/retry-failed"},
	}
}

// TestWebhookHandler_EveryRouteGatesOnRequireProject: no session redirects to login, and a member of another org gets 404 with no endpoint data.
func TestWebhookHandler_EveryRouteGatesOnRequireProject(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	e := x.create(t)
	require.NoError(t, x.Hooks.SendTest(x.ctx(), e.ID))
	did := x.Outbox.Entries()[0].ID.String()

	outsider := uuid.New()
	other := x.seedOrg(t, "other", outsider)
	stranger := session.Principal{UserID: outsider, ActiveOrgID: other.ID}

	for _, rt := range webhookRoutes(e.ID.String(), did) {
		rec := httptest.NewRecorder()
		x.Mux.ServeHTTP(rec, httptest.NewRequest(rt.method, rt.path, nil))
		assert.Equal(t, http.StatusSeeOther, rec.Code, "anonymous %s %s", rt.method, rt.path)
		assert.Equal(t, "/login", rec.Header().Get("Location"))

		rec = x.doAs(t, rt.method, rt.path, endpointForm("https://evil.example/hook", string(events.PostPublished)), stranger)
		assert.Equal(t, http.StatusNotFound, rec.Code, "outsider %s %s", rt.method, rt.path)
		assert.NotContains(t, rec.Body.String(), "https://example.com/hook")
	}

	items, err := x.Hooks.List(x.ctx())
	require.NoError(t, err)
	require.Len(t, items, 1, "no outsider request may create, update or delete")
	assert.Equal(t, "https://example.com/hook", items[0].URL)
}

// TestWebhookHandler_ASiblingProjectsEndpointIs404 pins the service's project check behind the console.
func TestWebhookHandler_ASiblingProjectsEndpointIs404(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	e := x.create(t)
	_, err := x.Projects.Create(setTenant(context.Background(), x.org, x.uid), x.org, "beta", "Beta")
	require.NoError(t, err)

	rec := x.do(t, http.MethodGet, "/orgs/acme/projects/beta/webhooks/"+e.ID.String(), nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestWebhookHandler_HTMXCreatePushesTheDetailURL swaps the detail in with the secret and pushes its URL, so a reload is a GET.
func TestWebhookHandler_HTMXCreatePushesTheDetailURL(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	x.Cfg.HTTP.BasePath = "/app"

	rec := x.doHX(t, http.MethodPost, webhookBase, endpointForm("https://example.com/hook", string(events.PostPublished)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	items, err := x.Hooks.List(x.ctx())
	require.NoError(t, err)
	require.Len(t, items, 1)
	detail := webhookBase + "/" + items[0].ID.String()
	assert.Equal(t, "/app"+detail, rec.Header().Get(web.HeaderPushURL))
	body := rec.Body.String()
	secret := reSecret.FindString(body)
	require.NotEmpty(t, secret)
	assert.NotContains(t, body, "<html", "an htmx create answers with the page fragment")
	assert.Contains(t, body, `id="webhook-page"`)

	rec = x.do(t, http.MethodGet, detail, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), secret)
	assert.Contains(t, rec.Body.String(), templates.WebhookSecretMask)
}

// TestWebhookHandler_HTMXCreateErrorRendersTheNewPageFragment keeps the URL and shows the code.
func TestWebhookHandler_HTMXCreateErrorRendersTheNewPageFragment(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)

	rec := x.doHX(t, http.MethodPost, webhookBase, endpointForm("http://example.com/hook", string(events.PostPublished)))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get(web.HeaderPushURL))
	body := rec.Body.String()
	assert.NotContains(t, body, "<html")
	assert.Contains(t, body, `id="webhook-page"`)
	assert.Contains(t, body, apperror.CodeWebhookInvalidURL)
	assert.Equal(t, "WHK002", apperror.CodeWebhookInvalidURL)
	assert.Contains(t, body, `name="event_types"`, "the fragment is the new page with its form")
}

// TestWebhookHandler_HTMXRotateRotatesOnce swaps the detail in with the new secret, pushes nothing, and leaves the original secret as the one secondary.
func TestWebhookHandler_HTMXRotateRotatesOnce(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	var got http.Header
	var body []byte
	rcv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		body, _ = io.ReadAll(r.Body)
	}))
	t.Cleanup(rcv.Close)
	e, original, err := x.Hooks.Create(x.ctx(), rcv.URL+"/hook", "", []events.Type{events.PostPublished})
	require.NoError(t, err)

	rec := x.doHX(t, http.MethodPost, webhookBase+"/"+e.ID.String()+"/rotate", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, rec.Header().Get(web.HeaderPushURL))
	assert.NotContains(t, rec.Body.String(), "<html")
	rotated := reSecret.FindString(rec.Body.String())
	require.NotEmpty(t, rotated)
	require.NotEqual(t, original, rotated)

	require.NoError(t, x.Hooks.SendTest(x.ctx(), e.ID))
	claimed, err := x.Outbox.ClaimDue(x.ctx(), time.Now(), 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	d := webhook.NewDeliverer(x.Store, x.Sealer, rcv.Client(), discardLogger())
	require.NoError(t, d.Deliver(setTenant(context.Background(), x.org, x.uid), claimed[0]))

	ts := got.Get("X-Opensheet-Timestamp")
	want := []string{webhook.Sign(rotated, ts, body), webhook.Sign(original, ts, body)}
	assert.ElementsMatch(t, want, strings.Fields(got.Get("X-Opensheet-Signature")),
		"exactly one rotation: the new primary plus the original secret as the secondary")
}

// TestWebhookHandler_AttemptCopy covers "not attempted yet" and the separator between runs after a Retry.
func TestWebhookHandler_AttemptCopy(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	e := x.create(t)
	require.NoError(t, x.Hooks.SendTest(x.ctx(), e.ID))
	entry := x.Outbox.Entries()[0]
	detail := webhookBase + "/" + e.ID.String()

	rec := x.do(t, http.MethodGet, detail, nil)
	assert.Contains(t, rec.Body.String(), "webhooks.not_attempted")
	assert.NotContains(t, rec.Body.String(), "0/8")

	base := time.Now().Add(-time.Hour)
	for i, n := range []int{1, 2, 1} {
		require.NoError(t, x.Store.SaveAttempt(x.ctx(), webhook.Attempt{
			OrgID: x.org, ProjectID: x.project.ID, EndpointID: e.ID, DeliveryID: entry.ID, EventID: entry.EventID,
			EventType: events.WebhookPing, Attempt: n, StatusCode: 500, CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}))
	}
	rec = x.do(t, http.MethodGet, detail+"/deliveries/"+entry.ID.String(), nil)
	body := rec.Body.String()
	assert.Equal(t, 1, strings.Count(body, "data-attempt-run-break"), body)
	assert.Less(t, strings.Index(body, "data-attempt-run-break"), strings.LastIndex(body, "2/8"))
}

func TestWebhookHandler_DeliveryShowsThePayloadAndHeaders(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	e := x.create(t)
	require.NoError(t, x.Hooks.SendTest(x.ctx(), e.ID))
	entry := x.Outbox.Entries()[0]

	rec := x.do(t, http.MethodGet, webhookBase+"/"+e.ID.String()+"/deliveries/"+entry.ID.String(), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := html.UnescapeString(rec.Body.String())

	var pretty bytes.Buffer
	require.NoError(t, json.Indent(&pretty, entry.Payload, "", "  "))
	require.Contains(t, pretty.String(), "\n", "the fixture payload must pretty-print onto several lines")
	assert.Contains(t, body, pretty.String(), "the body is pretty-printed server-side")
	assert.Contains(t, body, `data-copy="`+pretty.String()+`"`, "the copy button carries the shown body")
	for _, want := range []string{
		"X-Opensheet-Event-Id", "evt_" + entry.EventID.String(),
		"X-Opensheet-Event-Type", string(events.WebhookPing),
		"X-Opensheet-Delivery-Id", "dlv_" + entry.ID.String(),
		"Content-Type", "application/json",
		"User-Agent", "Opensheet-Webhooks/1",
		"webhooks.headers_signature_note",
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, "X-Opensheet-Signature", "per-attempt headers are never shown")
	assert.Less(t, strings.Index(body, "webhooks.payload_heading"), strings.Index(body, "webhooks.attempts_heading"),
		"the payload sits above the attempts")

	page := x.do(t, http.MethodGet, webhookBase+"/"+e.ID.String(), nil)
	assert.Contains(t, page.Body.String(), "opensheetCopyBound", "the detail page binds the copy handler the fragment's button needs")
}

func TestWebhookHandler_DeliveryFallsBackToTheRawBody(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	e := x.create(t)
	id := uuid.Must(uuid.NewV7())
	require.NoError(t, x.Outbox.Enqueue(x.ctx(), outbox.Entry{
		ID: id, EventID: uuid.Must(uuid.NewV7()), ProjectID: x.project.ID, Target: e.ID.String(),
		Payload: []byte("not json <b>raw</b>"),
	}))

	rec := x.do(t, http.MethodGet, webhookBase+"/"+e.ID.String()+"/deliveries/"+id.String(), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "not json &lt;b&gt;raw&lt;/b&gt;", "the raw body is shown, escaped")
	assert.NotContains(t, body, "<b>raw</b>")
	assert.NotContains(t, body, "webhooks.headers_heading", "an undecodable envelope was never sent, so no headers to show")
	assert.NotContains(t, body, "X-Opensheet-Event-Id")
}

func TestWebhookHandler_DeliveryOutsideTheListedWindowStillLoads(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	e := x.create(t)
	for range webhook.MaxDeliveriesListed + 1 {
		require.NoError(t, x.Hooks.SendTest(x.ctx(), e.ID))
	}
	all, err := x.Outbox.ListByTarget(x.ctx(), e.ID.String(), outbox.MaxListLimit)
	require.NoError(t, err)
	oldest := all[len(all)-1]

	rec := x.do(t, http.MethodGet, webhookBase+"/"+e.ID.String()+"/deliveries/"+oldest.ID.String(), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "dlv_"+oldest.ID.String())
}

func TestWebhookHandler_DeliveryOfAnotherScopeIs404(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	e := x.create(t)
	other := x.create(t)
	require.NoError(t, x.Hooks.SendTest(x.ctx(), e.ID))
	entry := x.Outbox.Entries()[0]
	path := webhookBase + "/" + e.ID.String() + "/deliveries/" + entry.ID.String()

	outsider := uuid.New()
	org := x.seedOrg(t, "other", outsider)
	rec := x.doAs(t, http.MethodGet, path, nil, session.Principal{UserID: outsider, ActiveOrgID: org.ID})
	assert.Equal(t, http.StatusNotFound, rec.Code, "another org")
	assert.NotContains(t, rec.Body.String(), "dlv_"+entry.ID.String())

	rec = x.do(t, http.MethodGet, webhookBase+"/"+other.ID.String()+"/deliveries/"+entry.ID.String(), nil)
	assert.Equal(t, http.StatusNotFound, rec.Code, "another endpoint's delivery")
	assert.NotContains(t, rec.Body.String(), "dlv_"+entry.ID.String())

	rec = x.do(t, http.MethodGet, webhookBase+"/"+e.ID.String()+"/deliveries/"+uuid.NewString(), nil)
	assert.Equal(t, http.StatusNotFound, rec.Code, "an unknown delivery")
}

// TestWebhookHandler_RetryOfAnUnlistedRowRetargetsTheDeliveries avoids swapping a bare row for one the page does not show.
func TestWebhookHandler_RetryOfAnUnlistedRowRetargetsTheDeliveries(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	e := x.create(t)
	for range webhook.MaxDeliveriesListed + 1 {
		require.NoError(t, x.Hooks.SendTest(x.ctx(), e.ID))
	}
	x.settle(t, true)
	all, err := x.Outbox.ListByTarget(x.ctx(), e.ID.String(), outbox.MaxListLimit)
	require.NoError(t, err)
	oldest := all[len(all)-1].ID.String()

	rec := x.doHX(t, http.MethodPost, webhookBase+"/"+e.ID.String()+"/deliveries/"+oldest+"/retry", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "#webhook-deliveries", rec.Header().Get(web.HeaderRetarget))
	assert.Equal(t, "outerHTML", rec.Header().Get(web.HeaderReswap))
	assert.Contains(t, rec.Body.String(), `id="webhook-deliveries"`)
}

// TestWebhookHandler_ActionFailuresUseTheActionCopy keeps "could not save" off a failed send.
func TestWebhookHandler_ActionFailuresUseTheActionCopy(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	e := x.create(t)
	x.Outbox.EnqueueErr = errors.New("outbox down")

	rec := x.do(t, http.MethodPost, webhookBase+"/"+e.ID.String()+"/test", nil)
	assert.Contains(t, rec.Body.String(), "webhooks.error.action_failed")
	assert.NotContains(t, rec.Body.String(), "webhooks.error.failed")
}

// TestWebhookHandler_RefusalsAreNotLoggedAsErrors logs only failures that are ours.
func TestWebhookHandler_RefusalsAreNotLoggedAsErrors(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	var buf bytes.Buffer
	x.Deps.Logger = log.New(&buf, "", 0)
	x.Mux = http.NewServeMux()
	handlers.NewWebhookHandler(x.Deps, x.Projects, x.Hooks).Register(x.Mux)
	e := x.create(t)

	x.do(t, http.MethodPost, webhookBase, endpointForm("http://example.com/hook", string(events.PostPublished)))
	x.do(t, http.MethodPost, webhookBase, endpointForm("https://example.com/hook"))
	_, err := x.Hooks.Update(x.ctx(), e.ID, e.URL, "", e.EventTypes, false)
	require.NoError(t, err)
	x.do(t, http.MethodPost, webhookBase+"/"+e.ID.String()+"/test", nil)
	assert.Empty(t, buf.String(), "a rejected input or a refused action is not an error log")

	_, err = x.Hooks.Update(x.ctx(), e.ID, e.URL, "", e.EventTypes, true)
	require.NoError(t, err)
	x.Outbox.EnqueueErr = errors.New("outbox down")
	x.do(t, http.MethodPost, webhookBase+"/"+e.ID.String()+"/test", nil)
	assert.Contains(t, buf.String(), "web webhook: send test")
}

// TestWebhookErrorRules pins every rule of the webhook error table: its key, whether it is a refusal, and its max.
func TestWebhookErrorRules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err     error
		key     string
		refusal bool
		max     int
	}{
		{&webhook.InvalidURLError{Reason: "r"}, "webhooks.error.invalid_url", true, webhook.MaxURLLen},
		{&webhook.InvalidEventTypesError{Reason: "r"}, "webhooks.error.invalid_event_types", true, 0},
		{&webhook.InvalidDescriptionError{Reason: "r"}, "webhooks.error.invalid_description", true, webhook.MaxDescriptionRunes},
		{&webhook.EndpointLimitError{Limit: webhook.MaxEndpointsPerProject}, "webhooks.error.limit", true, webhook.MaxEndpointsPerProject},
		{&webhook.NotFoundError{ID: "x"}, "webhooks.error.not_found", true, 0},
		{&webhook.DeliveryNotFoundError{ID: "x"}, "webhooks.error.delivery_not_found", true, 0},
		{&webhook.DeliveryNotRetryableError{ID: "x"}, "webhooks.error.not_retryable", true, 0},
		{&webhook.EndpointInactiveError{ID: "x"}, "webhooks.error.inactive", true, 0},
		{&webhook.SecretConflictError{ID: "x"}, "webhooks.error.secret_conflict", true, 0},
		{&sealer.UnavailableError{}, "webhooks.error.sealer_unavailable", false, 0},
	}
	require.Len(t, tests, handlers.WebhookErrorRuleCount(), "every rule needs a case here")
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			key, refusal, maxLen := handlers.WebhookErrorRuleOf(fmt.Errorf("wrapped: %w", tt.err))
			assert.Equal(t, tt.key, key)
			assert.Equal(t, tt.refusal, refusal)
			assert.Equal(t, tt.max, maxLen)
		})
	}

	key, refusal, maxLen := handlers.WebhookErrorRuleOf(errors.New("boom"))
	assert.Equal(t, "webhooks.error.failed", key)
	assert.False(t, refusal)
	assert.Zero(t, maxLen)
}

// TestWebhookHandler_IntegrationGuide opens the steps on an empty list and puts the verifiers on the endpoint page.
func TestWebhookHandler_IntegrationGuide(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)

	rec := x.do(t, http.MethodGet, webhookBase, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `data-webhook-guide open`, "an empty project lands on the open guide")
	assert.Contains(t, body, "webhooks.guide_step_secret")
	assert.Contains(t, body, webhook.HeaderDeliveryID)
	assert.Contains(t, body, webhook.HeaderEventID)
	assert.NotContains(t, body, "data-webhook-verify")

	e := x.create(t)
	rec = x.do(t, http.MethodGet, webhookBase, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "data-webhook-guide")
	assert.NotContains(t, rec.Body.String(), `data-webhook-guide open`, "the guide folds away once an endpoint exists")

	rec = x.do(t, http.MethodGet, webhookBase+"/"+e.ID.String(), nil)
	require.Equal(t, http.StatusOK, rec.Code)
	body = rec.Body.String()
	assert.Contains(t, body, `<details data-webhook-verify class=`, "the verify section is a collapsed <details>")
	assert.Regexp(t, `<details data-webhook-verify[^>]*><summary[^>]*>webhooks.verify_heading</summary>`, body, "the summary is the section heading")
	assert.NotContains(t, body, `<details data-webhook-verify open`)
	assert.Regexp(t, `<nav role="tablist"[^>]*>`, body, "languages switch through a tab bar")
	assert.NotContains(t, body, `name="webhook-verify-lang"`, "a language is a tab, not a <details>")
	snippets, err := webhook.VerifySnippets()
	require.NoError(t, err)
	require.NotEmpty(t, snippets)
	for i, s := range snippets {
		tab := regexp.MustCompile(`<button type="button" role="tab" id="webhook-verify-tab-` + s.Lang + `" aria-controls="webhook-verify-panel-` + s.Lang + `" aria-selected="` + strconv.FormatBool(i == 0) + `"`)
		assert.Regexp(t, tab, body, "%s tab", s.Lang)
		assert.Contains(t, body, html.EscapeString(s.Code), "%s snippet renders escaped in <code>", s.Lang)
	}
	panels := regexp.MustCompile(`<div role="tabpanel"[^>]*>`).FindAllString(body, -1)
	require.Len(t, panels, len(snippets))
	visible := 0
	for _, p := range panels {
		if !strings.Contains(p, " hidden") {
			visible++
			assert.Contains(t, p, `data-snippet="`+webhook.SnippetGo+`"`, "Go shows first, with or without JS")
		}
	}
	assert.Equal(t, 1, visible, "exactly one panel is visible")
	assert.Contains(t, body, "window.opensheetTabsBound", "the tab switcher script ships nonced with the panel")
	assert.Contains(t, body, "(function() {\n\tif (window.opensheetTabsBound) return;", "the tab switcher keeps its helpers out of window")
	for _, h := range []string{webhook.HeaderTimestamp, webhook.HeaderSignature, webhook.HeaderDeliveryID, webhook.HeaderEventID} {
		assert.Contains(t, body, ">"+h+"</code>", "%s is quoted from the webhook constant", h)
	}
}

func TestWebhookHandler_DeliveryShowsEachAttemptsResponse(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t, false)
	e := x.create(t)
	require.NoError(t, x.Hooks.SendTest(x.ctx(), e.ID))
	entry := x.Outbox.Entries()[0]

	base := time.Now().Add(-time.Hour)
	for i, a := range []webhook.Attempt{
		{Attempt: 1, Error: "dial tcp: connection refused"},
		{Attempt: 2, StatusCode: 500, ResponseBody: "<b>x</b>", ResponseTruncated: true,
			ResponseHeaders: []webhook.Header{{Name: "X-Request-Id", Value: "<i>req_1</i>"}}},
		{Attempt: 3, StatusCode: 204},
		{Attempt: 4, StatusCode: 200, ResponseBody: `{"ok":true}`},
	} {
		a.OrgID, a.ProjectID, a.EndpointID, a.DeliveryID, a.EventID = x.org, x.project.ID, e.ID, entry.ID, entry.EventID
		a.EventType = events.WebhookPing
		a.CreatedAt = base.Add(time.Duration(i) * time.Minute)
		require.NoError(t, x.Store.SaveAttempt(x.ctx(), a))
	}

	rec := x.do(t, http.MethodGet, webhookBase+"/"+e.ID.String()+"/deliveries/"+entry.ID.String(), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()

	assert.Equal(t, 3, strings.Count(body, "data-attempt-response"), "an attempt without a response shows only its error")
	assert.Contains(t, body, "&lt;b&gt;x&lt;/b&gt;", "the response body is escaped")
	assert.NotContains(t, body, "<b>x</b>")
	assert.Contains(t, body, "X-Request-Id")
	assert.Contains(t, body, "&lt;i&gt;req_1&lt;/i&gt;", "header values are escaped")
	assert.NotContains(t, body, "<i>req_1</i>")
	assert.Equal(t, 1, strings.Count(body, "webhooks.response_truncated"), body)
	assert.Equal(t, 1, strings.Count(body, "webhooks.response_empty"), body)
	assert.Contains(t, html.UnescapeString(body), "{\n  \"ok\": true\n}", "a JSON response is pretty-printed")
	assert.Contains(t, html.UnescapeString(body), `data-copy="<b>x</b>"`, "the copy button carries the raw body")
	assert.Contains(t, body, "dial tcp: connection refused")
}

func TestWebhookCopy_NumbersComeFromParams(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	for _, loc := range b.All() {
		tr := b.For(loc)
		truncated := tr.T("webhooks.response_truncated", "KiB", 97)
		assert.Contains(t, truncated, "97", "%s: the cap comes from MaxResponseBodyBytes", loc)
		assert.NotContains(t, truncated, "4", "%s: no hard-coded cap", loc)

		retries := tr.T("webhooks.guide_retries", "Attempts", 91, "TotalHours", 92, "FirstSeconds", 93, "LongestHours", 94)
		for _, n := range []string{"91", "92", "93", "94"} {
			assert.Contains(t, retries, n, "%s: guide_retries", loc)
		}
		for _, hard := range []string{"30", "10"} {
			assert.NotContains(t, retries, hard, "%s: no hard-coded schedule", loc)
		}
	}
}
