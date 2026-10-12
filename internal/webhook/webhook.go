// Package webhook is the outbound webhooks bounded context: tenant endpoints and their delivery attempts.
package webhook

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/events"
)

// MaxEndpointsPerProject bounds the fan-out of one event.
const MaxEndpointsPerProject = 10

// MaxURLLen bounds an endpoint URL, in bytes.
const MaxURLLen = 2048

// MaxDescriptionRunes bounds an endpoint description.
const MaxDescriptionRunes = 200

// Endpoint is the aggregate root: one tenant URL subscribed to a set of event types.
type Endpoint struct {
	ID, OrgID, ProjectID uuid.UUID
	URL, Description     string
	EventTypes           []events.Type
	Secrets              SealedSecrets
	Active               bool
	CreatedAt, UpdatedAt time.Time
}

// SealedSecrets holds the signing secrets as sealed bytes; Secondary is nil outside a rotation.
type SealedSecrets struct{ Primary, Secondary []byte }

// Attempt records one delivery attempt of an outbox entry to an endpoint.
type Attempt struct {
	ID, OrgID, ProjectID, EndpointID, DeliveryID, EventID uuid.UUID
	EventType                                             events.Type
	Attempt                                               int
	StatusCode                                            int
	Error                                                 string
	ResponseBody                                          string
	ResponseTruncated                                     bool
	ResponseHeaders                                       []Header
	Duration                                              time.Duration
	CreatedAt                                             time.Time
}

// New enforces creation invariants and returns an active endpoint without secrets.
func New(orgID, projectID uuid.UUID, rawURL, description string, types []events.Type) (*Endpoint, error) {
	u, d, ts, err := validate(rawURL, description, types)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &Endpoint{
		ID:          uuid.Must(uuid.NewV7()),
		OrgID:       orgID,
		ProjectID:   projectID,
		URL:         u,
		Description: d,
		EventTypes:  ts,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// Update re-validates and replaces the editable fields, leaving the secrets alone.
func (e *Endpoint) Update(rawURL, description string, types []events.Type, active bool) error {
	u, d, ts, err := validate(rawURL, description, types)
	if err != nil {
		return err
	}
	e.URL = u
	e.Description = d
	e.EventTypes = ts
	e.Active = active
	e.UpdatedAt = time.Now().UTC()
	return nil
}

// SetSecrets replaces the sealed signing secrets.
func (e *Endpoint) SetSecrets(s SealedSecrets) {
	e.Secrets = s
	e.UpdatedAt = time.Now().UTC()
}

// Subscribes reports whether the endpoint is subscribed to t.
func (e *Endpoint) Subscribes(t events.Type) bool {
	return slices.Contains(e.EventTypes, t)
}

func validate(rawURL, description string, types []events.Type) (u, d string, ts []events.Type, err error) {
	if u, err = validateURL(rawURL); err != nil {
		return "", "", nil, err
	}
	if d, err = validateDescription(description); err != nil {
		return "", "", nil, err
	}
	if ts, err = validateTypes(types); err != nil {
		return "", "", nil, err
	}
	return u, d, ts, nil
}

func validateURL(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", &InvalidURLError{Reason: "is required"}
	}
	if len(rawURL) > MaxURLLen {
		return "", &InvalidURLError{Reason: "is longer than " + strconv.Itoa(MaxURLLen) + " characters"}
	}
	if !utf8.ValidString(rawURL) {
		return "", &InvalidURLError{Reason: "is not valid UTF-8"}
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", &InvalidURLError{Reason: "cannot be parsed"}
	}
	if u.Scheme != "https" {
		return "", &InvalidURLError{Reason: "must use https"}
	}
	if u.User != nil {
		return "", &InvalidURLError{Reason: "must not carry credentials"}
	}
	if u.Hostname() == "" {
		return "", &InvalidURLError{Reason: "has no host"}
	}
	return rawURL, nil
}

func validateDescription(description string) (string, error) {
	description = strings.TrimSpace(description)
	if !utf8.ValidString(description) {
		return "", &InvalidDescriptionError{Reason: "is not valid UTF-8"}
	}
	if utf8.RuneCountInString(description) > MaxDescriptionRunes {
		return "", &InvalidDescriptionError{Reason: "is longer than " + strconv.Itoa(MaxDescriptionRunes) + " characters"}
	}
	return description, nil
}

func validateTypes(types []events.Type) ([]events.Type, error) {
	if len(types) == 0 {
		return nil, &InvalidEventTypesError{Reason: "at least one event type is required"}
	}
	for i, t := range types {
		spec, ok := events.Lookup(t)
		if !ok {
			return nil, &InvalidEventTypesError{Reason: "unknown event type " + string(t)}
		}
		if !spec.Subscribable {
			return nil, &InvalidEventTypesError{Reason: "event type " + string(t) + " cannot be subscribed to"}
		}
		if slices.Contains(types[:i], t) {
			return nil, &InvalidEventTypesError{Reason: "event type " + string(t) + " is listed twice"}
		}
	}
	return slices.Clone(types), nil
}
