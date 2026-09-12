package credential

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/platform/tenant"
)

// ConnectWorkflow turns a Google consent into a sealed google_oauth credential.
type ConnectWorkflow struct {
	store      Store
	sealer     sealer.Sealer
	connector  *gworkspace.Connector
	secret     []byte
	now        func() time.Time
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
}

// NewConnectWorkflow binds the workflow to its dependencies; a nil now defaults to time.Now in UTC.
func NewConnectWorkflow(
	store Store,
	sl sealer.Sealer,
	connector *gworkspace.Connector,
	secret []byte,
	now func() time.Time,
	log *slog.Logger,
	unexpected apperror.UnexpectedFunc,
) *ConnectWorkflow {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &ConnectWorkflow{
		store:      store,
		sealer:     sl,
		connector:  connector,
		secret:     secret,
		now:        now,
		log:        log.With("module", "credential"),
		unexpected: unexpected,
	}
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
	return w.connector.AuthCodeURL(signed), nil
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

	grant, err := w.connector.Exchange(ctx, code)
	if err != nil {
		span.RecordError(err)
		return nil, w.unexpected(ctx, "credential.Connect.Complete: exchange", err,
			"org_id", st.OrgID, "project_id", st.ProjectID)
	}
	if grant.RefreshToken == "" {
		return nil, &NoRefreshTokenError{}
	}
	if grant.AccountEmail == "" {
		w.log.WarnContext(ctx, "google returned no account email; the credential will list without one",
			"org_id", st.OrgID, "project_id", st.ProjectID)
	}

	existing, err := w.existing(ctx, st.OrgID, st.ProjectID, grant.AccountEmail)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	id := uuid.Must(uuid.NewV7())
	if existing != nil {
		id = existing.ID
	}
	sealed, err := w.sealer.Seal([]byte(grant.RefreshToken), SealAAD(st.OrgID, st.ProjectID, id))
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	span.SetAttributes(attribute.String("credential.id", id.String()))

	if existing != nil {
		if rotateErr := existing.Rotate(sealed, grant.AccountEmail); rotateErr != nil {
			span.RecordError(rotateErr)
			return nil, rotateErr
		}
		return w.save(ctx, existing)
	}
	fresh, err := New(id, st.OrgID, st.ProjectID, st.UserID,
		connectName(id, grant.AccountEmail), KindGoogleOAuth, grant.AccountEmail, sealed)
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
	if w.connector == nil || len(w.secret) == 0 {
		return &NotConfiguredError{}
	}
	return nil
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
