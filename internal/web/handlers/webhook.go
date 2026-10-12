package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/events"
	"altalune.id/opensheet/internal/platform/outbox"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/templates"
	"altalune.id/opensheet/internal/webhook"
)

// WebhookHandler owns the project-scoped outbound webhook console pages.
type WebhookHandler struct {
	Deps
	Webhooks *webhook.Service
}

// NewWebhookHandler wires the handler.
func NewWebhookHandler(d Deps, projects *project.Service, hooks *webhook.Service) *WebhookHandler {
	d.Projects = projects
	return &WebhookHandler{Deps: d, Webhooks: hooks}
}

// Register wires the webhook routes onto mux.
func (h *WebhookHandler) Register(mux web.Mux) {
	const base = "/orgs/{org}/projects/{project}/webhooks"
	mux.HandleFunc("GET "+base, h.GetWebhooks)
	mux.HandleFunc("GET "+base+"/new", h.GetWebhookNew)
	mux.HandleFunc("POST "+base, h.PostWebhookCreate)
	mux.HandleFunc("GET "+base+"/{id}", h.GetWebhook)
	mux.HandleFunc("POST "+base+"/{id}", h.PostWebhookUpdate)
	mux.HandleFunc("POST "+base+"/{id}/delete", h.PostWebhookDelete)
	mux.HandleFunc("POST "+base+"/{id}/rotate", h.PostWebhookRotate)
	mux.HandleFunc("POST "+base+"/{id}/retire", h.PostWebhookRetire)
	mux.HandleFunc("POST "+base+"/{id}/test", h.PostWebhookTest)
	mux.HandleFunc("GET "+base+"/{id}/deliveries/{did}", h.GetWebhookDelivery)
	mux.HandleFunc("POST "+base+"/{id}/deliveries/{did}/retry", h.PostWebhookRetry)
	mux.HandleFunc("POST "+base+"/{id}/retry-failed", h.PostWebhookRetryFailed)
}

// GetWebhooks renders the endpoint list page.
func (h *WebhookHandler) GetWebhooks(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	items, err := h.Webhooks.List(sc.req.Context())
	if err != nil {
		h.LogErr("web webhook: list", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "List failed", "Could not load webhook endpoints.", err)
		return
	}
	rows := make([]templates.WebhookRow, 0, len(items))
	for _, e := range items {
		rows = append(rows, webhookRow(e))
	}
	v := templates.WebhooksView{
		ProjectSlug: sc.project.Slug,
		Items:       rows,
		Limit:       webhook.MaxEndpointsPerProject,
		Guide:       webhookGuide(len(rows) == 0),
	}
	Render(w, sc.req, templates.WebhooksLayout(h.layout(sc, "Webhooks · "+sc.project.Name), v))
}

// GetWebhookNew renders the empty endpoint form.
func (h *WebhookHandler) GetWebhookNew(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	h.writeNewForm(w, sc, webhookForm("", "", "", true, nil), templates.WebhookError{})
}

// PostWebhookCreate creates an endpoint and renders its detail page with the one-time secret.
func (h *WebhookHandler) PostWebhookCreate(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, sc.req, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	rawURL, desc, types := endpointFields(r)
	e, secret, err := h.Webhooks.Create(sc.req.Context(), rawURL, desc, types)
	if err != nil {
		h.logFailure("web webhook: create", err)
		h.writeNewForm(w, sc, webhookForm("", rawURL, desc, true, types), webhookError(err))
		return
	}
	if web.IsHTMXRequest(r) {
		w.Header().Set(web.HeaderPushURL, h.ProjectURL(sc, "/webhooks/"+e.ID.String()))
	}
	// SECURITY: the plaintext secret is rendered into this one response and discarded — never logged, stored or redirected with.
	h.writeDetail(w, sc, e, detailOpts{secret: secret})
}

// GetWebhook renders the endpoint detail page with its recent deliveries.
func (h *WebhookHandler) GetWebhook(w http.ResponseWriter, r *http.Request) {
	sc, e, ok := h.requireEndpoint(w, r)
	if !ok {
		return
	}
	h.writeDetail(w, sc, e, detailOpts{})
}

// PostWebhookUpdate replaces the editable fields and redirects to the detail page.
func (h *WebhookHandler) PostWebhookUpdate(w http.ResponseWriter, r *http.Request) {
	sc, e, ok := h.requireEndpoint(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, sc.req, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	rawURL, desc, types := endpointFields(r)
	active := r.PostForm.Get("active") == "1"
	if _, err := h.Webhooks.Update(sc.req.Context(), e.ID, rawURL, desc, types, active); err != nil {
		h.logFailure("web webhook: update", err)
		form := webhookForm(e.ID.String(), rawURL, desc, active, types)
		h.writeDetail(w, sc, e, detailOpts{form: &form, err: webhookError(err)})
		return
	}
	h.redirect(w, sc, "/webhooks/"+e.ID.String())
}

// PostWebhookDelete removes the endpoint and redirects to the list.
func (h *WebhookHandler) PostWebhookDelete(w http.ResponseWriter, r *http.Request) {
	sc, e, ok := h.requireEndpoint(w, r)
	if !ok {
		return
	}
	if err := h.Webhooks.Delete(sc.req.Context(), e.ID); err != nil && !webhook.IsNotFoundError(err) {
		h.logFailure("web webhook: delete", err)
		h.writeDetail(w, sc, e, detailOpts{err: webhookError(err)})
		return
	}
	h.redirect(w, sc, "/webhooks")
}

// PostWebhookRotate issues a new primary secret and renders the detail page with it, once.
func (h *WebhookHandler) PostWebhookRotate(w http.ResponseWriter, r *http.Request) {
	sc, e, ok := h.requireEndpoint(w, r)
	if !ok {
		return
	}
	secret, err := h.Webhooks.Rotate(sc.req.Context(), e.ID)
	if err != nil {
		h.logFailure("web webhook: rotate", err)
		h.writeDetail(w, sc, e, detailOpts{err: webhookError(err)})
		return
	}
	h.reloadDetail(w, sc, e.ID, detailOpts{secret: secret})
}

// PostWebhookRetire drops the secondary secret and redirects to the detail page.
func (h *WebhookHandler) PostWebhookRetire(w http.ResponseWriter, r *http.Request) {
	sc, e, ok := h.requireEndpoint(w, r)
	if !ok {
		return
	}
	if err := h.Webhooks.RetireSecondary(sc.req.Context(), e.ID); err != nil {
		h.logFailure("web webhook: retire secondary", err)
		h.writeDetail(w, sc, e, detailOpts{err: webhookError(err)})
		return
	}
	h.redirect(w, sc, "/webhooks/"+e.ID.String())
}

// PostWebhookTest queues a webhook.ping and returns the refreshed deliveries fragment.
func (h *WebhookHandler) PostWebhookTest(w http.ResponseWriter, r *http.Request) {
	sc, e, ok := h.requireEndpoint(w, r)
	if !ok {
		return
	}
	if err := h.Webhooks.SendTest(sc.req.Context(), e.ID); err != nil {
		h.logFailure("web webhook: send test", err)
		h.writeDeliveries(w, sc, e, templates.WebhookNotice{}, webhookActionError(err))
		return
	}
	//i18n:use webhooks.test_queued
	h.writeDeliveries(w, sc, e, templates.WebhookNotice{Key: "webhooks.test_queued"}, templates.WebhookError{})
}

// GetWebhookDelivery returns the payload and attempts fragment of one delivery.
func (h *WebhookHandler) GetWebhookDelivery(w http.ResponseWriter, r *http.Request) {
	sc, e, did, ok := h.requireDelivery(w, r)
	if !ok {
		return
	}
	d, err := h.Webhooks.Delivery(sc.req.Context(), e.ID, did)
	if err != nil {
		if webhook.IsDeliveryNotFoundError(err) || webhook.IsNotFoundError(err) {
			h.ErrorPage(w, sc.req, http.StatusNotFound, "Not found", "That delivery no longer exists.", err)
			return
		}
		h.LogErr("web webhook: delivery", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "Load failed", "Could not load the delivery.", err)
		return
	}
	items, err := h.Webhooks.Attempts(sc.req.Context(), e.ID, did)
	if err != nil {
		h.LogErr("web webhook: attempts", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "Load failed", "Could not load the delivery attempts.", err)
		return
	}
	rows := make([]templates.WebhookAttemptRow, 0, len(items))
	for _, a := range items {
		rows = append(rows, templates.WebhookAttemptRow{
			Attempt:    a.Attempt,
			StatusCode: a.StatusCode,
			Error:      a.Error,
			Duration:   a.Duration.Round(time.Millisecond).String(),
			CreatedAt:  a.CreatedAt.UTC().Format(time.RFC3339),
			Response:   responseView(a),
		})
	}
	Render(w, sc.req, templates.WebhookDeliveryDetail(h.ProjectFragmentBase(sc), payloadView(d), rows, outbox.MaxAttempts))
}

// PostWebhookRetry requeues one failed delivery and returns its row fragment.
func (h *WebhookHandler) PostWebhookRetry(w http.ResponseWriter, r *http.Request) {
	sc, e, did, ok := h.requireDelivery(w, r)
	if !ok {
		return
	}
	retryErr := h.Webhooks.Retry(sc.req.Context(), e.ID, did)
	if retryErr != nil {
		h.logFailure("web webhook: retry", retryErr)
	}
	v, err := h.deliveriesView(sc, e)
	if err != nil {
		h.LogErr("web webhook: deliveries", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "Load failed", "Could not load deliveries.", err)
		return
	}
	i := slices.IndexFunc(v.Items, func(d templates.WebhookDeliveryRow) bool { return d.ID == did.String() })
	if i < 0 {
		if retryErr != nil {
			v.Error = webhookActionError(retryErr)
		}
		w.Header().Set(web.HeaderRetarget, "#webhook-deliveries")
		w.Header().Set(web.HeaderReswap, "outerHTML")
		Render(w, sc.req, templates.WebhookDeliveries(h.ProjectFragmentBase(sc), v))
		return
	}
	row := v.Items[i]
	if retryErr != nil {
		row.Error = webhookActionError(retryErr)
	}
	Render(w, sc.req, templates.WebhookRetryResult(h.ProjectFragmentBase(sc), v, row))
}

// PostWebhookRetryFailed requeues every failed delivery and returns the refreshed deliveries fragment.
func (h *WebhookHandler) PostWebhookRetryFailed(w http.ResponseWriter, r *http.Request) {
	sc, e, ok := h.requireEndpoint(w, r)
	if !ok {
		return
	}
	n, err := h.Webhooks.RetryFailed(sc.req.Context(), e.ID)
	if err != nil {
		h.logFailure("web webhook: retry failed", err)
		h.writeDeliveries(w, sc, e, templates.WebhookNotice{}, webhookActionError(err))
		return
	}
	//i18n:use webhooks.retried
	h.writeDeliveries(w, sc, e, templates.WebhookNotice{Key: "webhooks.retried", Count: n}, templates.WebhookError{})
}

type detailOpts struct {
	secret string
	form   *templates.WebhookFormView
	err    templates.WebhookError
}

func (h *WebhookHandler) requireEndpoint(w http.ResponseWriter, r *http.Request) (ProjectScope, *webhook.Endpoint, bool) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return ProjectScope{}, nil, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.ErrorPage(w, sc.req, http.StatusBadRequest, "Bad id", "Malformed webhook endpoint id.")
		return ProjectScope{}, nil, false
	}
	e, err := h.Webhooks.ByID(sc.req.Context(), id)
	if err != nil {
		if webhook.IsNotFoundError(err) {
			h.ErrorPage(w, sc.req, http.StatusNotFound, "Not found", "That webhook endpoint no longer exists.", err)
			return ProjectScope{}, nil, false
		}
		h.LogErr("web webhook: byID", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "Lookup failed", "Could not load that webhook endpoint.", err)
		return ProjectScope{}, nil, false
	}
	return sc, e, true
}

func (h *WebhookHandler) requireDelivery(w http.ResponseWriter, r *http.Request) (ProjectScope, *webhook.Endpoint, uuid.UUID, bool) {
	sc, e, ok := h.requireEndpoint(w, r)
	if !ok {
		return ProjectScope{}, nil, uuid.Nil, false
	}
	did, err := uuid.Parse(r.PathValue("did"))
	if err != nil {
		h.ErrorPage(w, sc.req, http.StatusBadRequest, "Bad id", "Malformed delivery id.")
		return ProjectScope{}, nil, uuid.Nil, false
	}
	return sc, e, did, true
}

func (h *WebhookHandler) reloadDetail(w http.ResponseWriter, sc ProjectScope, id uuid.UUID, o detailOpts) {
	e, err := h.Webhooks.ByID(sc.req.Context(), id)
	if err != nil {
		h.LogErr("web webhook: byID", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "Lookup failed", "Could not load that webhook endpoint.", err)
		return
	}
	h.writeDetail(w, sc, e, o)
}

func (h *WebhookHandler) writeDetail(w http.ResponseWriter, sc ProjectScope, e *webhook.Endpoint, o detailOpts) {
	deliveries, err := h.deliveriesView(sc, e)
	if err != nil {
		h.LogErr("web webhook: deliveries", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "Load failed", "Could not load deliveries.", err)
		return
	}
	form := webhookForm(e.ID.String(), e.URL, e.Description, e.Active, e.EventTypes)
	if o.form != nil {
		form = *o.form
	}
	form.ProjectSlug, form.Error = sc.project.Slug, o.err
	guide := webhookGuide(false)
	snippets, err := webhook.VerifySnippets()
	if err != nil {
		h.LogErr("web webhook: verify snippets", err)
	}
	for _, s := range snippets {
		guide.Snippets = append(guide.Snippets, templates.WebhookSnippet{Lang: s.Lang, Label: s.Label, Code: s.Code})
	}
	v := templates.WebhookDetailView{
		ProjectSlug: sc.project.Slug,
		Endpoint:    webhookRow(e),
		Secret:      o.secret,
		Form:        form,
		Deliveries:  deliveries,
		Guide:       guide,
	}
	if web.IsHTMXRequest(sc.req) {
		Render(w, sc.req, templates.WebhookDetailPage(h.ProjectFragmentBase(sc), v))
		return
	}
	Render(w, sc.req, templates.WebhookDetailLayout(h.layout(sc, "Webhook · "+sc.project.Name), v))
}

func (h *WebhookHandler) writeNewForm(w http.ResponseWriter, sc ProjectScope, form templates.WebhookFormView, werr templates.WebhookError) {
	form.ProjectSlug, form.Error = sc.project.Slug, werr
	if web.IsHTMXRequest(sc.req) {
		Render(w, sc.req, templates.WebhookNewPage(h.ProjectFragmentBase(sc), form))
		return
	}
	Render(w, sc.req, templates.WebhookNewLayout(h.layout(sc, "New webhook · "+sc.project.Name), form))
}

func (h *WebhookHandler) writeDeliveries(w http.ResponseWriter, sc ProjectScope, e *webhook.Endpoint, notice templates.WebhookNotice, werr templates.WebhookError) {
	v, err := h.deliveriesView(sc, e)
	if err != nil {
		h.LogErr("web webhook: deliveries", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "Load failed", "Could not load deliveries.", err)
		return
	}
	v.Notice, v.Error = notice, werr
	Render(w, sc.req, templates.WebhookDeliveries(h.ProjectFragmentBase(sc), v))
}

func (h *WebhookHandler) deliveriesView(sc ProjectScope, e *webhook.Endpoint) (templates.WebhookDeliveriesView, error) {
	items, err := h.Webhooks.Deliveries(sc.req.Context(), e.ID, webhook.MaxDeliveriesListed)
	if err != nil {
		return templates.WebhookDeliveriesView{}, err
	}
	v := templates.WebhookDeliveriesView{
		ProjectSlug: sc.project.Slug,
		EndpointID:  e.ID.String(),
		Active:      e.Active,
		MaxAttempts: outbox.MaxAttempts,
		Items:       make([]templates.WebhookDeliveryRow, 0, len(items)),
	}
	for _, d := range items {
		row := deliveryRow(d)
		if row.Failed {
			v.FailedCount++
		}
		v.Items = append(v.Items, row)
	}
	return v, nil
}

func webhookGuide(open bool) templates.WebhookGuideView {
	first, longest, total := retrySchedule()
	return templates.WebhookGuideView{
		EventIDHeader:    webhook.HeaderEventID,
		DeliveryIDHeader: webhook.HeaderDeliveryID,
		TimestampHeader:  webhook.HeaderTimestamp,
		SignatureHeader:  webhook.HeaderSignature,
		MaxAttempts:      outbox.MaxAttempts,
		TotalRetryHours:  int(total.Round(time.Hour) / time.Hour),
		FirstWaitSeconds: int(first / time.Second),
		LongestWaitHours: int(longest.Round(time.Hour) / time.Hour),
		Open:             open,
	}
}

func retrySchedule() (first, longest, total time.Duration) { //nolint:nonamedreturns // three durations differ in role
	first = outbox.BackoffBase(2)
	for attempt := 2; attempt <= outbox.MaxAttempts; attempt++ {
		wait := outbox.BackoffBase(attempt)
		longest = max(longest, wait)
		total += wait
	}
	return first, longest, total
}

func (h *WebhookHandler) layout(sc ProjectScope, title string) web.LayoutData {
	return h.LayoutForProject(sc.req, title, sc.org.Slug, sc.project, "webhooks")
}

func (h *WebhookHandler) redirect(w http.ResponseWriter, sc ProjectScope, suffix string) {
	http.Redirect(w, sc.req, h.ProjectURL(sc, suffix), http.StatusSeeOther) //nolint:gosec // G710: both slugs come from resolved rows and suffix is built from a parsed uuid
}

func endpointFields(r *http.Request) (rawURL, desc string, types []events.Type) { //nolint:nonamedreturns // three strings differ in role
	return strings.TrimSpace(r.PostForm.Get("url")),
		strings.TrimSpace(r.PostForm.Get("description")),
		selectedEventTypes(r.PostForm["event_types"])
}

// SECURITY: a value that is not a subscribable catalog type, or a duplicate, is dropped rather than reaching the service.
func selectedEventTypes(raw []string) []events.Type {
	set := make(map[string]bool, len(raw))
	for _, s := range raw {
		set[strings.TrimSpace(s)] = true
	}
	out := make([]events.Type, 0, len(raw))
	for _, s := range events.Subscribable() {
		if set[string(s.Type)] {
			out = append(out, s.Type)
		}
	}
	return out
}

func webhookForm(id, rawURL, desc string, active bool, selected []events.Type) templates.WebhookFormView {
	subs := events.Subscribable()
	opts := make([]templates.WebhookEventOption, 0, len(subs))
	for _, s := range subs {
		opts = append(opts, templates.WebhookEventOption{
			Value:    string(s.Type),
			LabelKey: eventLabelKey(s.Type),
			Checked:  slices.Contains(selected, s.Type),
		})
	}
	return templates.WebhookFormView{
		ID:             id,
		URL:            rawURL,
		Description:    desc,
		Active:         active,
		Events:         opts,
		MaxURLLen:      webhook.MaxURLLen,
		MaxDescription: webhook.MaxDescriptionRunes,
	}
}

func webhookRow(e *webhook.Endpoint) templates.WebhookRow {
	labels := make([]string, 0, len(e.EventTypes))
	for _, t := range e.EventTypes {
		labels = append(labels, eventLabelKey(t))
	}
	return templates.WebhookRow{
		ID:             e.ID.String(),
		URL:            e.URL,
		Description:    e.Description,
		EventLabelKeys: labels,
		Active:         e.Active,
		Rotating:       e.Secrets.Secondary != nil,
		CreatedAt:      e.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func deliveryRow(d webhook.Delivery) templates.WebhookDeliveryRow {
	row := templates.WebhookDeliveryRow{
		ID:             d.ID.String(),
		EventLabelKey:  eventLabelKey(d.EventType),
		StatusLabelKey: statusLabelKey(d.Status),
		Status:         string(d.Status),
		Attempt:        d.Attempt,
		LastError:      d.LastError,
		CreatedAt:      d.CreatedAt.UTC().Format(time.RFC3339),
		Failed:         d.Status == outbox.StatusFailed,
	}
	if d.Status == outbox.StatusPending && d.Attempt > 0 {
		row.NextRetry = d.NextAttemptAt.UTC().Format(time.RFC3339)
	}
	return row
}

func payloadView(d webhook.Delivery) templates.WebhookPayloadView {
	v := templates.WebhookPayloadView{Body: prettyPayload(d.Payload)}
	if d.EventType == "" {
		return v
	}
	v.Headers = headerRows(webhook.DeliveryHeaders(d))
	return v
}

func responseView(a webhook.Attempt) templates.WebhookResponseView {
	return templates.WebhookResponseView{
		Body:      a.ResponseBody,
		Pretty:    prettyPayload([]byte(a.ResponseBody)),
		Truncated: a.ResponseTruncated,
		LimitKiB:  webhook.MaxResponseBodyBytes >> 10,
		Headers:   headerRows(a.ResponseHeaders),
	}
}

func headerRows(headers []webhook.Header) []templates.WebhookHeaderRow {
	rows := make([]templates.WebhookHeaderRow, len(headers))
	for i, hd := range headers {
		rows[i] = templates.WebhookHeaderRow{Name: hd.Name, Value: hd.Value}
	}
	return rows
}

func prettyPayload(b []byte) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, b, "", "  "); err != nil {
		return string(b)
	}
	return buf.String()
}

//i18n:use webhooks.event.*
func eventLabelKey(t events.Type) string {
	if _, ok := events.Lookup(t); !ok {
		return "webhooks.event.unknown"
	}
	return "webhooks.event." + strings.ReplaceAll(string(t), ".", "_")
}

//i18n:use webhooks.status.*
func statusLabelKey(s outbox.Status) string {
	if !s.Valid() {
		return ""
	}
	return "webhooks.status." + string(s)
}

// NOTE: a rejected input or a refused action is the caller's mistake, not ours, so only the rest is logged.
func (h *WebhookHandler) logFailure(msg string, err error) {
	if isWebhookRefusal(err) {
		return
	}
	h.LogErr(msg, err)
}

func isWebhookRefusal(err error) bool {
	return lookupWebhookError(err).refusal
}

func webhookActionError(err error) templates.WebhookError {
	e := webhookError(err)
	if e.Key == webhookErrorFailed {
		e.Key = "webhooks.error.action_failed"
	}
	return e
}

func webhookError(err error) templates.WebhookError {
	rule := lookupWebhookError(err)
	return templates.WebhookError{Key: rule.key, Code: ErrorRef(err), Max: rule.max}
}

const webhookErrorFailed = "webhooks.error.failed"

type webhookErrorRule struct {
	match   func(error) bool
	key     string
	refusal bool
	max     int
}

//i18n:use webhooks.error.*
func webhookErrorRules() []webhookErrorRule {
	return []webhookErrorRule{
		{webhook.IsInvalidURLError, "webhooks.error.invalid_url", true, webhook.MaxURLLen},
		{webhook.IsInvalidEventTypesError, "webhooks.error.invalid_event_types", true, 0},
		{webhook.IsInvalidDescriptionError, "webhooks.error.invalid_description", true, webhook.MaxDescriptionRunes},
		{webhook.IsEndpointLimitError, "webhooks.error.limit", true, webhook.MaxEndpointsPerProject},
		{webhook.IsNotFoundError, "webhooks.error.not_found", true, 0},
		{webhook.IsDeliveryNotFoundError, "webhooks.error.delivery_not_found", true, 0},
		{webhook.IsDeliveryNotRetryableError, "webhooks.error.not_retryable", true, 0},
		{webhook.IsEndpointInactiveError, "webhooks.error.inactive", true, 0},
		{webhook.IsSecretConflictError, "webhooks.error.secret_conflict", true, 0},
		{sealer.IsUnavailableError, "webhooks.error.sealer_unavailable", false, 0},
	}
}

func lookupWebhookError(err error) webhookErrorRule {
	for _, rule := range webhookErrorRules() {
		if rule.match(err) {
			return rule
		}
	}
	return webhookErrorRule{key: webhookErrorFailed}
}
