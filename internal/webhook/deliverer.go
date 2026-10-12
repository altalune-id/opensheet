package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/opensheet/internal/platform/events"
	"altalune.id/opensheet/internal/platform/outbox"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/platform/tenant"
)

// Delivery header names a receiver reads.
const (
	HeaderEventID    = "X-Opensheet-Event-Id"
	HeaderEventType  = "X-Opensheet-Event-Type"
	HeaderDeliveryID = "X-Opensheet-Delivery-Id"
	HeaderTimestamp  = "X-Opensheet-Timestamp"
	HeaderSignature  = "X-Opensheet-Signature"
)

const (
	headerContentType = "Content-Type"
	headerUserAgent   = "User-Agent"

	contentTypeJSON = "application/json"
	userAgent       = "Opensheet-Webhooks/1"

	attemptSaveTimeout = 5 * time.Second
)

var _ outbox.Deliverer = (*Deliverer)(nil)

// Header is one HTTP header name and value sent with a delivery.
type Header struct {
	Name  string
	Value string
}

// DeliveryHeaders returns the non-secret headers every attempt of d carries; the timestamp and signature are computed per attempt and never stored.
func DeliveryHeaders(d Delivery) []Header {
	return deliveryHeaders(d.Entry, d.EventType)
}

func deliveryHeaders(e outbox.Entry, eventType events.Type) []Header {
	return []Header{
		{Name: HeaderEventID, Value: eventIDPrefix + e.EventID.String()},
		{Name: HeaderEventType, Value: string(eventType)},
		{Name: HeaderDeliveryID, Value: deliveryIDPrefix + e.ID.String()},
		{Name: headerContentType, Value: contentTypeJSON},
		{Name: headerUserAgent, Value: userAgent},
	}
}

// Deliverer is the outbox.Deliverer that signs and POSTs one webhook delivery.
type Deliverer struct {
	store  Store
	sealer sealer.Sealer
	client *http.Client
	log    *slog.Logger
}

// NewDeliverer copies client and refuses its redirects, so a 3xx answer counts as a failed delivery.
func NewDeliverer(store Store, sl sealer.Sealer, client *http.Client, log *slog.Logger) *Deliverer {
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Deliverer{store: store, sealer: sl, client: &c, log: log.With("module", "webhook")}
}

// Deliver sends e to its endpoint and records the attempt; a deleted endpoint or a cross-project row settles without sending.
func (d *Deliverer) Deliver(ctx context.Context, e outbox.Entry) error {
	ctx, span := tracer.Start(ctx, "webhook.Deliver", trace.WithAttributes(
		attribute.String("endpoint.id", e.Target),
		attribute.String("delivery.id", e.ID.String()),
		attribute.Int("webhook.attempt", e.Attempt),
	))
	defer span.End()

	err := d.deliver(ctx, e)
	if err == nil {
		return nil
	}
	logged := redactedError(err)
	span.RecordError(errors.New(logged))
	if e.Attempt >= outbox.MaxAttempts {
		d.log.WarnContext(ctx, "webhook: delivery exhausted", append(deliveryAttrs(e), "err", logged)...)
	}
	return err
}

func (d *Deliverer) deliver(ctx context.Context, e outbox.Entry) error {
	endpointID, err := uuid.Parse(e.Target)
	if err != nil {
		return fmt.Errorf("webhook.Deliver: target: %w", err)
	}
	ep, err := d.store.ByID(ctx, endpointID)
	if IsNotFoundError(err) {
		d.log.InfoContext(ctx, "webhook: endpoint deleted, delivery skipped", deliveryAttrs(e)...)
		return nil
	}
	if err != nil {
		return fmt.Errorf("webhook.Deliver: load endpoint: %w", err)
	}
	// SECURITY: a row whose scope disagrees with its endpoint is corrupt and must never cross projects.
	if ep.OrgID != e.OrgID || ep.ProjectID != e.ProjectID {
		d.log.ErrorContext(ctx, "webhook: endpoint scope mismatch, delivery dropped",
			append(deliveryAttrs(e), "endpoint_project_id", ep.ProjectID.String())...)
		return nil
	}
	ctx = tenant.WithProject(ctx, e.ProjectID)
	if !ep.Active {
		return &EndpointInactiveError{ID: ep.ID.String()}
	}
	secrets, err := d.openSecrets(ep)
	if err != nil {
		return err
	}
	eventType, err := envelopeType(e.Payload)
	if err != nil {
		return fmt.Errorf("webhook.Deliver: %w", err)
	}

	start := time.Now()
	rc, sendErr := d.post(ctx, ep.URL, e, eventType, secrets)
	d.saveAttempt(ctx, Attempt{
		OrgID:             e.OrgID,
		ProjectID:         e.ProjectID,
		EndpointID:        ep.ID,
		DeliveryID:        e.ID,
		EventID:           e.EventID,
		EventType:         eventType,
		Attempt:           e.Attempt,
		StatusCode:        rc.status,
		Error:             errorText(sendErr),
		ResponseBody:      rc.body,
		ResponseTruncated: rc.truncated,
		ResponseHeaders:   rc.headers,
		Duration:          time.Since(start),
	}, e)
	return sendErr
}

func (d *Deliverer) openSecrets(ep *Endpoint) ([]string, error) {
	primary, err := openSecret(d.sealer, ep.ID, slotPrimary, ep.Secrets.Primary)
	if err != nil {
		return nil, &SecretUnavailableError{Cause: err}
	}
	if len(ep.Secrets.Secondary) == 0 {
		return []string{primary}, nil
	}
	secondary, err := openSecret(d.sealer, ep.ID, slotSecondary, ep.Secrets.Secondary)
	if err != nil {
		return nil, &SecretUnavailableError{Cause: err}
	}
	return []string{primary, secondary}, nil
}

func (d *Deliverer) post(ctx context.Context, endpointURL string, e outbox.Entry, eventType events.Type, secrets []string) (receipt, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(e.Payload))
	if err != nil {
		return receipt{}, fmt.Errorf("webhook.Deliver: build request: %w", err)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signatures := make([]string, len(secrets))
	for i, s := range secrets {
		signatures[i] = Sign(s, timestamp, e.Payload)
	}
	for _, h := range deliveryHeaders(e, eventType) {
		req.Header.Set(h.Name, h.Value)
	}
	req.Header.Set(HeaderTimestamp, timestamp)
	req.Header.Set(HeaderSignature, strings.Join(signatures, " "))

	resp, err := d.client.Do(req)
	if err != nil {
		return receipt{}, fmt.Errorf("webhook.Deliver: post: %w", err)
	}
	rc := readReceipt(resp)
	_ = resp.Body.Close()
	if rc.status < 200 || rc.status > 299 {
		return rc, &DeliveryFailedError{StatusCode: rc.status}
	}
	return rc, nil
}

func (d *Deliverer) saveAttempt(ctx context.Context, a Attempt, e outbox.Entry) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), attemptSaveTimeout)
	defer cancel()
	if err := d.store.SaveAttempt(ctx, a); err != nil {
		d.log.ErrorContext(ctx, "webhook: record attempt", append(deliveryAttrs(e), "err", err.Error())...)
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// SECURITY: an endpoint URL may carry a query-string token, so operator logs and spans never see it.
func redactedError(err error) string {
	if failed, ok := errors.AsType[*DeliveryFailedError](err); ok {
		return failed.Error()
	}
	if u, ok := errors.AsType[*url.Error](err); ok {
		return u.Op + ": " + u.Err.Error()
	}
	return err.Error()
}

func deliveryAttrs(e outbox.Entry) []any {
	return []any{
		"org_id", e.OrgID.String(),
		"project_id", e.ProjectID.String(),
		"endpoint_id", e.Target,
		"delivery_id", e.ID.String(),
	}
}
