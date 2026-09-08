package credential

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/oauth2"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/platform/tenant"
)

// The Google scopes the connect flow requests.
// NOTE: drive.file is non-sensitive, so no verification review and no 7-day refresh-token expiry; email is the only way Complete learns the account.
// https://developers.google.com/workspace/sheets/api/scopes
const (
	scopeDriveFile = "https://www.googleapis.com/auth/drive.file"
	scopeEmail     = "email"
)

const exchangeTimeout = 20 * time.Second

// Scopes returns the default scope set the connect flow requests.
func Scopes() []string { return []string{scopeDriveFile, scopeEmail} }

// ConnectWorkflow turns a Google consent into a sealed google_oauth credential.
type ConnectWorkflow struct {
	store      Store
	sealer     sealer.Sealer
	oauth      *oauth2.Config
	secret     []byte
	scopes     []string
	now        func() time.Time
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
}

// ConnectOption tunes ConnectWorkflow construction.
type ConnectOption func(*ConnectWorkflow)

// WithScopes replaces the Google scope set the connect flow requests; an empty set keeps Scopes().
func WithScopes(scopes []string) ConnectOption {
	return func(w *ConnectWorkflow) {
		if len(scopes) > 0 {
			w.scopes = slices.Clone(scopes)
		}
	}
}

// NewConnectWorkflow binds the workflow to its dependencies; a nil now defaults to time.Now in UTC.
func NewConnectWorkflow(
	store Store,
	sl sealer.Sealer,
	oauth *oauth2.Config,
	secret []byte,
	now func() time.Time,
	log *slog.Logger,
	unexpected apperror.UnexpectedFunc,
	opts ...ConnectOption,
) *ConnectWorkflow {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	w := &ConnectWorkflow{
		store:      store,
		sealer:     sl,
		oauth:      oauth,
		secret:     secret,
		scopes:     Scopes(),
		now:        now,
		log:        log.With("module", "credential"),
		unexpected: unexpected,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Start returns the Google consent URL, carrying a signed state that binds the grant to one org, project and user.
func (w *ConnectWorkflow) Start(
	ctx context.Context,
	orgID, projectID, userID uuid.UUID,
	returnTo string,
) (string, error) {
	ctx, span := tracer.Start(ctx, "credential.Connect.Start")
	defer span.End()

	if err := w.configured(); err != nil {
		return "", err
	}
	if orgID == uuid.Nil || projectID == uuid.Nil || userID == uuid.Nil {
		return "", &tenant.UnscopedError{}
	}
	span.SetAttributes(
		attribute.String("org_id", orgID.String()),
		attribute.String("project_id", projectID.String()),
	)

	st, err := newState(orgID, projectID, userID, returnTo, w.now())
	if err != nil {
		span.RecordError(err)
		if IsStateInvalidError(err) {
			return "", err
		}
		return "", w.unexpected(ctx, "credential.Connect.Start: state", err,
			"org_id", orgID, "project_id", projectID)
	}
	signed, err := encodeState(w.secret, st)
	if err != nil {
		span.RecordError(err)
		return "", w.unexpected(ctx, "credential.Connect.Start: encode state", err,
			"org_id", orgID, "project_id", projectID)
	}

	// SECURITY: the scope set is pinned here from construction rather than read off the injected config, so a mis-wired client cannot widen it.
	// NOTE: prompt=consent is what makes access_type=offline return a refresh token on every reconnect, not only the first.
	return w.oauth.AuthCodeURL(signed,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("scope", strings.Join(w.scopes, " ")),
		oauth2.SetAuthURLParam("prompt", "consent"),
	), nil
}

// Complete verifies the signed state, exchanges the code and stores the refresh token sealed against its credential row.
// SECURITY: the refresh token never reaches a return value, an error message, a log field or a span attribute.
func (w *ConnectWorkflow) Complete(ctx context.Context, code, rawState string) (*Credential, error) {
	ctx, span := tracer.Start(ctx, "credential.Connect.Complete")
	defer span.End()

	if err := w.configured(); err != nil {
		return nil, err
	}
	st, err := decodeState(w.secret, rawState, w.now())
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	if strings.TrimSpace(code) == "" {
		return nil, &StateInvalidError{Reason: reasonNoCode}
	}
	span.SetAttributes(
		attribute.String("org_id", st.OrgID.String()),
		attribute.String("project_id", st.ProjectID.String()),
	)
	// NOTE: a Google callback carries no request scope, so this write scopes itself from the verified state.
	ctx = tenant.Into(ctx, tenant.Context{OrgID: st.OrgID, ProjectID: st.ProjectID, UserID: st.UserID})

	tok, err := w.exchange(ctx, code)
	if err != nil {
		span.RecordError(err)
		return nil, w.unexpected(ctx, "credential.Connect.Complete: exchange", err,
			"org_id", st.OrgID, "project_id", st.ProjectID)
	}
	refresh := strings.TrimSpace(tok.RefreshToken)
	if refresh == "" {
		return nil, &NoRefreshTokenError{}
	}

	email := accountEmail(tok)
	if email == "" {
		w.log.WarnContext(ctx, "google returned no account email; the credential will list without one",
			"org_id", st.OrgID, "project_id", st.ProjectID)
	}

	existing, err := w.existing(ctx, st.OrgID, st.ProjectID, email)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	id := uuid.Must(uuid.NewV7())
	if existing != nil {
		id = existing.ID
	}
	sealed, err := w.sealer.Seal([]byte(refresh), SealAAD(st.OrgID, st.ProjectID, id))
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	span.SetAttributes(attribute.String("credential.id", id.String()))

	if existing != nil {
		if rotateErr := existing.Rotate(sealed, email); rotateErr != nil {
			span.RecordError(rotateErr)
			return nil, rotateErr
		}
		return w.save(ctx, existing)
	}
	fresh, err := New(id, st.OrgID, st.ProjectID, st.UserID,
		connectName(id, email), KindGoogleOAuth, email, sealed)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	return w.save(ctx, fresh)
}

// ReturnTo recovers the post-connect redirect target from a signed state without consuming the authorization code.
func (w *ConnectWorkflow) ReturnTo(rawState string) (string, error) {
	if err := w.configured(); err != nil {
		return "", err
	}
	st, err := decodeState(w.secret, rawState, w.now())
	if err != nil {
		return "", err
	}
	return st.ReturnTo, nil
}

func (w *ConnectWorkflow) configured() error {
	if w.oauth == nil || w.oauth.ClientID == "" || w.oauth.ClientSecret == "" || len(w.secret) == 0 {
		return &NotConfiguredError{}
	}
	return nil
}

func (w *ConnectWorkflow) exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	ctx, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()

	tok, err := w.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("credential: connect: token exchange: %w", err)
	}
	return tok, nil
}

func (w *ConnectWorkflow) existing(
	ctx context.Context,
	orgID, projectID uuid.UUID,
	email string,
) (*Credential, error) {
	if email == "" {
		return nil, nil
	}
	all, err := w.store.List(ctx, orgID, projectID)
	if err != nil {
		return nil, w.unexpected(ctx, "credential.Connect.Complete: list", err,
			"org_id", orgID, "project_id", projectID)
	}
	for _, c := range all {
		if c.Kind == KindGoogleOAuth && c.GoogleAccountEmail == email {
			return c, nil
		}
	}
	return nil, nil
}

func (w *ConnectWorkflow) save(ctx context.Context, c *Credential) (*Credential, error) {
	if err := w.store.Save(ctx, c); err != nil {
		if IsAlreadyExistsError(err) {
			return nil, err
		}
		return nil, w.unexpected(ctx, "credential.Connect.Complete: save", err,
			"org_id", c.OrgID, "project_id", c.ProjectID, "credential_id", c.ID)
	}
	w.log.InfoContext(ctx, "google oauth credential connected",
		"credential_id", c.ID, "org_id", c.OrgID, "project_id", c.ProjectID)
	return c, nil
}

func connectName(id uuid.UUID, email string) string {
	if email != "" && utf8.RuneCountInString(email) <= MaxNameRunes {
		return email
	}
	// NOTE: the tail of a UUIDv7 is random; its leading digits are a timestamp that only moves every ~65s.
	s := id.String()
	return "Google account " + s[len(s)-8:]
}

// SECURITY: the id_token arrives inside the token-endpoint response over TLS, so the channel authenticates it;
// only the email claim is read and it is display-only, never an authorization input.
func accountEmail(tok *oauth2.Token) string {
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return ""
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return strings.TrimSpace(claims.Email)
}
