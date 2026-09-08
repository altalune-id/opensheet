package apikey

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/tenant"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/opensheet/internal/apikey")

// Sheets reports which sheets exist in a project, for validating a key's grant.
type Sheets interface {
	IDsInProject(ctx context.Context, orgID, projectID uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error)
}

// Service is the API keys driving port.
type Service struct {
	store         Store
	log           *slog.Logger
	unexpected    apperror.UnexpectedFunc
	sheets        Sheets
	dummyVerifies atomic.Uint64
}

// NewService binds the service to its dependencies.
func NewService(store Store, log *slog.Logger, unexpected apperror.UnexpectedFunc, sheets Sheets) *Service {
	return &Service{
		store:      store,
		log:        log.With("module", "apikey"),
		unexpected: unexpected,
		sheets:     sheets,
	}
}

// CreateRequest is the input to Create.
type CreateRequest struct {
	Name      string
	Scopes    []string
	SheetIDs  []uuid.UUID
	ExpiresAt *time.Time
}

// DummyVerifications reports how many times Authenticate took the constant-time unknown-prefix path.
func (s *Service) DummyVerifications() uint64 { return s.dummyVerifies.Load() }

// Create mints a key in the caller's tenant scope and returns the plaintext exactly once.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*APIKey, string, error) {
	ctx, span := tracer.Start(ctx, "apikey.Create")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, "", err
	}
	span.SetAttributes(
		attribute.String("org_id", tc.OrgID.String()),
		attribute.String("project_id", tc.ProjectID.String()),
	)

	for _, sc := range req.Scopes {
		if !authn.Valid(strings.TrimSpace(sc)) {
			return nil, "", &InvalidScopeError{Scope: sc, Reason: "not in catalog"}
		}
	}
	if err := s.checkGrant(ctx, tc, req.SheetIDs); err != nil {
		return nil, "", err
	}

	k, plaintext, err := Mint(tc.OrgID, tc.ProjectID, req.Name, req.Scopes, req.SheetIDs, req.ExpiresAt, time.Now().UTC())
	if err != nil {
		span.RecordError(err)
		if IsInvalidNameError(err) || IsInvalidScopeError(err) || IsInvalidExpiryError(err) {
			return nil, "", err
		}
		return nil, "", s.unexpected(ctx, "apikey.Create: mint", err,
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	if err := s.store.Save(ctx, k); err != nil {
		span.RecordError(err)
		if IsAlreadyExistsError(err) {
			return nil, "", err
		}
		return nil, "", s.unexpected(ctx, "apikey.Create: save", err,
			"org_id", tc.OrgID, "project_id", tc.ProjectID, "api_key_id", k.ID)
	}
	span.SetAttributes(attribute.String("apikey.id", k.ID.String()))
	return k, plaintext, nil
}

// List returns the keys of the caller's project. SECURITY: results carry no secret material.
func (s *Service) List(ctx context.Context) ([]*APIKey, error) {
	ctx, span := tracer.Start(ctx, "apikey.List")
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
		return nil, s.unexpected(ctx, "apikey.List: list", err,
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	return out, nil
}

// Revoke marks the identified key revoked; a revoked key never authenticates again.
func (s *Service) Revoke(ctx context.Context, id uuid.UUID) (*APIKey, error) {
	ctx, span := tracer.Start(ctx, "apikey.Revoke",
		trace.WithAttributes(attribute.String("apikey.id", id.String())))
	defer span.End()

	k, err := s.scoped(ctx, id)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	k.Revoke(time.Now().UTC())
	if err := s.store.Save(ctx, k); err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "apikey.Revoke: save", err, "api_key_id", id)
	}
	return k, nil
}

// Delete removes the identified key from the caller's tenant scope.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "apikey.Delete",
		trace.WithAttributes(attribute.String("apikey.id", id.String())))
	defer span.End()

	if _, err := s.scoped(ctx, id); err != nil {
		span.RecordError(err)
		return err
	}
	if err := s.store.Delete(ctx, id); err != nil {
		span.RecordError(err)
		return s.unexpected(ctx, "apikey.Delete: delete", err, "api_key_id", id)
	}
	return nil
}

// Authenticate resolves a presented raw key and returns it when it is usable.
// SECURITY: missing, malformed, unknown, revoked and expired keys all return the identical opaque *UnauthorizedError.
func (s *Service) Authenticate(ctx context.Context, raw string) (*APIKey, error) {
	ctx, span := tracer.Start(ctx, "apikey.Authenticate")
	defer span.End()

	prefix, secret, err := Parse(raw)
	if err != nil {
		return nil, &UnauthorizedError{}
	}

	k, err := s.store.ByPrefix(ctx, prefix)
	if err != nil {
		if !IsNotFoundError(err) {
			span.RecordError(err)
			return nil, s.unexpected(ctx, "apikey.Authenticate: byPrefix", err)
		}
		// SECURITY: an unknown prefix still pays for one hash comparison, so a miss costs the same as a hit.
		if dummyVerify(secret) {
			s.log.ErrorContext(ctx, "apikey: presented secret matched the dummy hash")
		}
		s.dummyVerifies.Add(1)
		return nil, &UnauthorizedError{}
	}
	if !k.Verify(secret) {
		return nil, &UnauthorizedError{}
	}
	if !k.Active(time.Now().UTC()) {
		return nil, &UnauthorizedError{}
	}
	// SECURITY: ByPrefix bypasses RLS by design, so a key resolved under another org's scope must not be honoured.
	if tc, tErr := tenant.From(ctx); tErr == nil && tc.OrgID != k.OrgID {
		return nil, &UnauthorizedError{}
	}
	// SECURITY: the digest has done its job; it must not travel out to the request layer with the principal.
	k.SecretHash = nil
	// TODO: the apikey-lastused worker (Phase 1b) owns writing LastUsedAt; the read path must not write.
	span.SetAttributes(
		attribute.String("apikey.id", k.ID.String()),
		attribute.String("org_id", k.OrgID.String()),
		attribute.String("project_id", k.ProjectID.String()),
	)
	return k, nil
}

func (s *Service) scoped(ctx context.Context, id uuid.UUID) (*APIKey, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	k, err := s.store.ByID(ctx, id)
	if err != nil {
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "apikey.scoped: byID", err, "api_key_id", id)
	}
	if k.OrgID != tc.OrgID || k.ProjectID != tc.ProjectID {
		return nil, &NotFoundError{ID: id.String()}
	}
	return k, nil
}

func (s *Service) checkGrant(ctx context.Context, tc tenant.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	if s.sheets == nil {
		return s.unexpected(ctx, "apikey.Create: grant", errors.New("apikey: sheets dependency is not wired"),
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	found, err := s.sheets.IDsInProject(ctx, tc.OrgID, tc.ProjectID, ids)
	if err != nil {
		return s.unexpected(ctx, "apikey.Create: sheet ids in project", err,
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	for _, id := range ids {
		if !slices.Contains(found, id) {
			return &UnknownSheetError{SheetID: id.String()}
		}
	}
	return nil
}
