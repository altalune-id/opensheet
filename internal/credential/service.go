package credential

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/oauth2/google"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/gsheets"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/platform/tenant"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/opensheet/internal/credential")

// Service is the credentials driving port.
type Service struct {
	store      Store
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
	sealer     sealer.Sealer
}

// NewService binds the service to its dependencies.
func NewService(store Store, log *slog.Logger, unexpected apperror.UnexpectedFunc, sl sealer.Sealer) *Service {
	return &Service{
		store:      store,
		log:        log.With("module", "credential"),
		unexpected: unexpected,
		sealer:     sl,
	}
}

// UploadServiceAccount seals an uploaded Google service-account key into a new credential.
// SECURITY: raw never reaches a return value, a log field or a span attribute.
func (s *Service) UploadServiceAccount(ctx context.Context, name string, raw []byte) (*Credential, error) {
	ctx, span := tracer.Start(ctx, "credential.UploadServiceAccount")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	span.SetAttributes(
		attribute.String("org_id", tc.OrgID.String()),
		attribute.String("project_id", tc.ProjectID.String()),
	)

	email, err := serviceAccountEmail(raw)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	id := uuid.Must(uuid.NewV7())
	sealed, err := s.sealer.Seal(raw, SealAAD(tc.OrgID, tc.ProjectID, id))
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	c, err := New(id, tc.OrgID, tc.ProjectID, tc.UserID, name, KindServiceAccount, email, sealed)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	if saveErr := s.store.Save(ctx, c); saveErr != nil {
		span.RecordError(saveErr)
		if IsAlreadyExistsError(saveErr) {
			return nil, saveErr
		}
		return nil, s.unexpected(ctx, "credential.UploadServiceAccount: save", saveErr,
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	span.SetAttributes(attribute.String("credential.id", c.ID.String()))
	s.log.InfoContext(ctx, "service account credential stored",
		"credential_id", c.ID, "org_id", tc.OrgID, "project_id", tc.ProjectID)
	return c, nil
}

// ByID returns the identified credential when it belongs to the caller's tenant scope.
func (s *Service) ByID(ctx context.Context, id uuid.UUID) (*Credential, error) {
	ctx, span := tracer.Start(ctx, "credential.ByID",
		trace.WithAttributes(attribute.String("credential.id", id.String())))
	defer span.End()

	return s.scoped(ctx, span, id, "credential.ByID")
}

// List returns the credentials in the caller's tenant scope.
func (s *Service) List(ctx context.Context) ([]*Credential, error) {
	ctx, span := tracer.Start(ctx, "credential.List")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	span.SetAttributes(
		attribute.String("org_id", tc.OrgID.String()),
		attribute.String("project_id", tc.ProjectID.String()),
	)

	out, err := s.store.List(ctx, tc.OrgID, tc.ProjectID)
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "credential.List: list", err,
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	return out, nil
}

// Delete removes the identified credential; a spreadsheet still referencing it fails with *InUseError.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "credential.Delete",
		trace.WithAttributes(attribute.String("credential.id", id.String())))
	defer span.End()

	if _, err := s.scoped(ctx, span, id, "credential.Delete"); err != nil {
		return err
	}
	if err := s.store.Delete(ctx, id); err != nil {
		span.RecordError(err)
		if IsInUseError(err) || IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, "credential.Delete: delete", err, "credential_id", id)
	}
	return nil
}

// MarkReauthNeeded records that Google rejected the credential and a human must reconnect it.
func (s *Service) MarkReauthNeeded(ctx context.Context, id uuid.UUID) (*Credential, error) {
	ctx, span := tracer.Start(ctx, "credential.MarkReauthNeeded",
		trace.WithAttributes(attribute.String("credential.id", id.String())))
	defer span.End()

	c, err := s.scoped(ctx, span, id, "credential.MarkReauthNeeded")
	if err != nil {
		return nil, err
	}
	c.MarkReauthNeeded()
	if saveErr := s.store.Save(ctx, c); saveErr != nil {
		span.RecordError(saveErr)
		return nil, s.unexpected(ctx, "credential.MarkReauthNeeded: save", saveErr, "credential_id", id)
	}
	return c, nil
}

// TokenSourceFor unseals the credential and returns a Google token source.
// SECURITY: plaintext never leaves this method — not in a return value, an error, a log field or a span attribute.
func (s *Service) TokenSourceFor(ctx context.Context, id uuid.UUID) (gsheets.TokenSource, error) {
	ctx, span := tracer.Start(ctx, "credential.TokenSourceFor",
		trace.WithAttributes(attribute.String("credential.id", id.String())))
	defer span.End()

	c, err := s.scoped(ctx, span, id, "credential.TokenSourceFor")
	if err != nil {
		return nil, err
	}
	if c.Status == StatusReauthNeeded {
		return nil, &ReauthNeededError{ID: id.String()}
	}
	if c.Kind != KindServiceAccount {
		// TODO: google_oauth token sources land with the connect flow in Phase 1b.
		return nil, &InvalidKindError{Kind: string(c.Kind)}
	}
	if len(c.Sealed) == 0 {
		return nil, &NotSealedError{Situation: "token source"}
	}

	plain, err := s.sealer.Open(c.Sealed, SealAAD(c.OrgID, c.ProjectID, c.ID))
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	cfg, err := google.JWTConfigFromJSON(plain, gsheets.ScopeReadOnly)
	if err != nil {
		// SECURITY: the google error quotes the payload, so it is dropped rather than wrapped.
		return nil, &InvalidServiceAccountError{Reason: "stored key is not a service account key"}
	}
	return cfg.TokenSource(ctx), nil
}

func (s *Service) scoped(ctx context.Context, span trace.Span, id uuid.UUID, op string) (*Credential, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	c, err := s.store.ByID(ctx, id)
	if err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, op+": byID", err, "credential_id", id)
	}
	if c.OrgID != tc.OrgID || c.ProjectID != tc.ProjectID {
		return nil, &NotFoundError{ID: id.String()}
	}
	return c, nil
}

// googleTokenHost is the only host a service-account key may be told to mint tokens against.
// SECURITY: an attacker-supplied token_uri would turn an upload into a server-side request to their host.
const googleTokenHost = "oauth2.googleapis.com" //nolint:gosec // a hostname, not a credential.

type serviceAccountFile struct {
	Type        string `json:"type"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// SECURITY: the credential type is pinned to service_account. An inferred type would accept an
// external_account config pointing at an attacker-controlled token URL.
func serviceAccountEmail(raw []byte) (string, error) {
	var f serviceAccountFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return "", &InvalidServiceAccountError{Reason: "not valid JSON"}
	}
	if f.Type != string(KindServiceAccount) {
		return "", &InvalidKindError{Kind: safeKind(f.Type)}
	}
	email := strings.TrimSpace(f.ClientEmail)
	if email == "" {
		return "", &InvalidServiceAccountError{Reason: "missing client_email"}
	}
	if strings.TrimSpace(f.PrivateKey) == "" {
		return "", &InvalidServiceAccountError{Reason: "missing private_key"}
	}
	if err := checkTokenURI(f.TokenURI); err != nil {
		return "", err
	}
	if _, err := google.JWTConfigFromJSON(raw, gsheets.ScopeReadOnly); err != nil {
		return "", &InvalidServiceAccountError{Reason: "missing a required service account field"}
	}
	return email, nil
}

func checkTokenURI(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return &InvalidServiceAccountError{Reason: "token_uri is not a URL"}
	}
	if u.Scheme != "https" || u.Host != googleTokenHost {
		return &InvalidServiceAccountError{Reason: "token_uri does not point at " + googleTokenHost}
	}
	return nil
}

// SECURITY: bounds what an uploaded payload can push into an error message.
func safeKind(s string) string {
	if s == "" {
		return "unknown"
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && r != '_' {
			return "unknown"
		}
	}
	if len(s) > 40 {
		return "unknown"
	}
	return s
}
