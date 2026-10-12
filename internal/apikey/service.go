package apikey

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/opensheet/internal/apikey")

// Members is the org membership gate: RequireManager guards org and project keys, RequireMember guards a personal token and its use.
type Members interface {
	RequireManager(ctx context.Context, orgID, userID uuid.UUID) error
	RequireMember(ctx context.Context, orgID, userID uuid.UUID) error
}

// Projects lists an org's project ids, so a grant can only name the org's own projects.
type Projects interface {
	ProjectIDs(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error)
}

// Service is the API key driving port. SECURITY: every caller-initiated write asks Members first — an owner or admin for an org or project key, the owner for a personal token.
type Service struct {
	store      Store
	scheme     Scheme
	members    Members
	projects   Projects
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
	now        func() time.Time
}

// NewService binds the service to its dependencies.
func NewService(store Store, scheme Scheme, members Members, projects Projects, log *slog.Logger, unexpected apperror.UnexpectedFunc) *Service {
	return &Service{
		store:      store,
		scheme:     scheme,
		members:    members,
		projects:   projects,
		log:        log.With("module", "apikey"),
		unexpected: unexpected,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// Mint creates a new project key in the caller's project and returns it with its one-time plaintext secret.
func (s *Service) Mint(ctx context.Context, name string, scopes []string, resourceIDs []uuid.UUID, expiresAt *time.Time) (*APIKey, string, error) {
	ctx, span := tracer.Start(ctx, "apikey.Mint")
	defer span.End()

	tc, err := s.mintGate(ctx, KindProject, scopes, expiresAt)
	if err != nil {
		span.RecordError(err)
		return nil, "", err
	}
	span.SetAttributes(
		attribute.String("org_id", tc.OrgID.String()),
		attribute.String("project_id", tc.ProjectID.String()),
	)
	k, plaintext, err := s.scheme.Mint(tc.OrgID, tc.ProjectID, name, scopes, resourceIDs, expiresAt, s.now())
	if err != nil {
		span.RecordError(err)
		return nil, "", err
	}
	return s.saveMinted(ctx, span, tc, k, plaintext)
}

// MintOrg creates a new org key reaching grant and returns it with its one-time plaintext secret.
func (s *Service) MintOrg(ctx context.Context, name string, scopes []string, grant ProjectGrant, expiresAt *time.Time) (*APIKey, string, error) {
	ctx, span := tracer.Start(ctx, "apikey.MintOrg")
	defer span.End()

	tc, err := s.mintGate(ctx, KindOrg, scopes, expiresAt)
	if err != nil {
		span.RecordError(err)
		return nil, "", err
	}
	span.SetAttributes(attribute.String("org_id", tc.OrgID.String()), attribute.Bool("apikey.all_projects", grant.All))
	if err := s.requireOrgProjects(ctx, tc.OrgID, grant.ProjectIDs); err != nil {
		span.RecordError(err)
		return nil, "", err
	}
	k, plaintext, err := s.scheme.MintOrg(tc.OrgID, name, scopes, grant, expiresAt, s.now())
	if err != nil {
		span.RecordError(err)
		return nil, "", err
	}
	return s.saveMinted(ctx, span, tc, k, plaintext)
}

// MintPersonal creates a personal access token for the caller inside their org, reaching grant.
func (s *Service) MintPersonal(ctx context.Context, name string, scopes []string, grant ProjectGrant, expiresAt *time.Time) (*APIKey, string, error) {
	ctx, span := tracer.Start(ctx, "apikey.MintPersonal")
	defer span.End()

	tc, err := s.mintGate(ctx, KindPersonal, scopes, expiresAt)
	if err == nil {
		err = s.requireOrgProjects(ctx, tc.OrgID, grant.ProjectIDs)
	}
	if err != nil {
		span.RecordError(err)
		return nil, "", err
	}
	span.SetAttributes(attribute.String("org_id", tc.OrgID.String()), attribute.Bool("apikey.all_projects", grant.All))
	k, plaintext, err := s.scheme.MintPersonal(tc.OrgID, tc.UserID, name, scopes, grant, expiresAt, s.now())
	if err != nil {
		span.RecordError(err)
		return nil, "", err
	}
	return s.saveMinted(ctx, span, tc, k, plaintext)
}

func (s *Service) saveMinted(ctx context.Context, span trace.Span, tc tenant.Context, k *APIKey, plaintext string) (*APIKey, string, error) {
	k.CreatedBy = tc.UserID
	if saveErr := s.store.Save(ctx, k); saveErr != nil {
		span.RecordError(saveErr)
		return nil, "", s.unexpected(ctx, "apikey.Mint: save", saveErr,
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	span.SetAttributes(attribute.String("apikey.id", k.ID.String()))
	return k, plaintext, nil
}

// List returns the project keys in projectID within the caller's tenant scope.
func (s *Service) List(ctx context.Context, projectID uuid.UUID) ([]*APIKey, error) {
	ctx, span := tracer.Start(ctx, "apikey.List")
	defer span.End()
	span.SetAttributes(attribute.String("project_id", projectID.String()))

	out, err := s.store.List(ctx, projectID)
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "apikey.List: list", err, "project_id", projectID)
	}
	return out, nil
}

// ListOrg returns the org keys of the caller's org.
func (s *Service) ListOrg(ctx context.Context) ([]*APIKey, error) {
	ctx, span := tracer.Start(ctx, "apikey.ListOrg")
	defer span.End()

	out, err := s.store.ListOrg(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "apikey.ListOrg: list", err)
	}
	return out, nil
}

// ListPersonal returns the caller's own personal access tokens in the caller's org.
func (s *Service) ListPersonal(ctx context.Context) ([]*APIKey, error) {
	ctx, span := tracer.Start(ctx, "apikey.ListPersonal")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.store.ListPersonal(ctx, tc.UserID)
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "apikey.ListPersonal: list", err)
	}
	return out, nil
}

// RevokePersonalOf permanently revokes every personal token userID holds in orgID; it is the org's member-removed hook. SECURITY: revoked is final; re-joining never revives a token.
func (s *Service) RevokePersonalOf(ctx context.Context, orgID, userID uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "apikey.RevokePersonalOf")
	defer span.End()
	span.SetAttributes(attribute.String("org_id", orgID.String()), attribute.String("user_id", userID.String()))

	if err := s.store.RevokePersonal(tenant.WithOrg(ctx, orgID), userID, s.now()); err != nil {
		span.RecordError(err)
		return s.unexpected(ctx, "apikey.RevokePersonalOf", err, "org_id", orgID, "user_id", userID)
	}
	return nil
}

// GrantProjects widens an org key's or personal token's selected projects by projectIDs.
func (s *Service) GrantProjects(ctx context.Context, id uuid.UUID, projectIDs []uuid.UUID) (*APIKey, error) {
	ctx, span := tracer.Start(ctx, "apikey.GrantProjects")
	defer span.End()
	span.SetAttributes(attribute.String("apikey.id", id.String()))

	return s.promote(ctx, id, func(tc tenant.Context, k *APIKey) error {
		if err := s.requireOrgProjects(ctx, tc.OrgID, projectIDs); err != nil {
			return err
		}
		return k.GrantProjects(projectIDs)
	})
}

// GrantAllProjects promotes an org key or personal token to every project of its org; the promotion cannot be undone.
func (s *Service) GrantAllProjects(ctx context.Context, id uuid.UUID) (*APIKey, error) {
	ctx, span := tracer.Start(ctx, "apikey.GrantAllProjects")
	defer span.End()
	span.SetAttributes(attribute.String("apikey.id", id.String()))

	return s.promote(ctx, id, func(_ tenant.Context, k *APIKey) error { return k.GrantAllProjects() })
}

func (s *Service) promote(ctx context.Context, id uuid.UUID, widen func(tenant.Context, *APIKey) error) (*APIKey, error) {
	tc, k, err := s.loadForChange(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := widen(tc, k); err != nil {
		return nil, err
	}
	if saveErr := s.store.Save(ctx, k); saveErr != nil {
		return nil, s.unexpected(ctx, "apikey.promote: save", saveErr, "apikey_id", id)
	}
	return k, nil
}

// Revoke marks a key permanently unusable in the caller's tenant scope.
func (s *Service) Revoke(ctx context.Context, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "apikey.Revoke")
	defer span.End()
	span.SetAttributes(attribute.String("apikey.id", id.String()))

	_, k, err := s.loadForChange(ctx, id)
	if err != nil {
		span.RecordError(err)
		return err
	}
	if k.RevokedAt == nil {
		now := s.now()
		k.RevokedAt = &now
	}
	if saveErr := s.store.Save(ctx, k); saveErr != nil {
		span.RecordError(saveErr)
		return s.unexpected(ctx, "apikey.Revoke: save", saveErr, "apikey_id", id)
	}
	return nil
}

// SECURITY: the one gate every mint passes — who may mint the kind, never a retired scope, and a lifetime within MaxLifetime.
func (s *Service) mintGate(ctx context.Context, kind Kind, scopes []string, expiresAt *time.Time) (tenant.Context, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return tenant.Context{}, err
	}
	if err := s.requireMinter(ctx, kind, tc, scopes); err != nil {
		return tenant.Context{}, err
	}
	if err := requireMintable(scopes); err != nil {
		return tenant.Context{}, err
	}
	return tc, validateLifetime(expiresAt, s.now())
}

func (s *Service) requireMinter(ctx context.Context, kind Kind, tc tenant.Context, scopes []string) error {
	if kind == KindPersonal {
		return requireOwner(ctx, s.members, tc.OrgID, tc.UserID, scopes)
	}
	return s.members.RequireManager(ctx, tc.OrgID, tc.UserID)
}

// SECURITY: a personal token belongs to its owner alone — to anyone else it reads as absent — and every other kind needs an owner or admin.
func (s *Service) loadForChange(ctx context.Context, id uuid.UUID) (tenant.Context, *APIKey, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return tenant.Context{}, nil, err
	}
	k, err := s.load(ctx, tc, id)
	if err != nil {
		return tenant.Context{}, nil, err
	}
	if k.Kind != KindPersonal {
		return tc, k, s.members.RequireManager(ctx, tc.OrgID, tc.UserID)
	}
	if tc.UserID == uuid.Nil || k.CreatedBy != tc.UserID {
		return tenant.Context{}, nil, &NotFoundError{}
	}
	return tc, k, s.members.RequireMember(ctx, tc.OrgID, tc.UserID)
}

func requireMintable(scopes []string) error {
	for _, sc := range scopes {
		if authn.Valid(sc) && !authn.Mintable(sc) {
			return &RetiredScopeError{Scope: sc}
		}
	}
	return nil
}

// SECURITY: the one owner rule, asked at mint and on every use — a member, or an owner or admin when the token holds an org-level scope.
func requireOwner(ctx context.Context, m Members, orgID, userID uuid.UUID, scopes []string) error {
	if hasOrgLevelScope(scopes) {
		return m.RequireManager(ctx, orgID, userID)
	}
	return m.RequireMember(ctx, orgID, userID)
}

func hasOrgLevelScope(scopes []string) bool {
	return slices.ContainsFunc(scopes, func(sc string) bool {
		level, ok := authn.LevelOf(sc)
		return ok && level == authn.LevelOrg
	})
}

// SECURITY: a key outside the caller's own project, or a granted key seen from a project, reports the same *NotFoundError as a missing id.
func (s *Service) load(ctx context.Context, tc tenant.Context, id uuid.UUID) (*APIKey, error) {
	k, err := s.store.ByID(ctx, id)
	if err != nil {
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "apikey.load", err, "apikey_id", id)
	}
	if k.OrgID != tc.OrgID || k.ProjectID != tc.ProjectID {
		return nil, &NotFoundError{}
	}
	return k, nil
}

func (s *Service) requireOrgProjects(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID) error {
	if len(projectIDs) == 0 {
		return nil
	}
	owned, err := s.projects.ProjectIDs(ctx, orgID)
	if err != nil {
		return s.unexpected(ctx, "apikey: list org projects", err, "org_id", orgID)
	}
	for _, id := range projectIDs {
		if !slices.Contains(owned, id) {
			return &ProjectNotInOrgError{ProjectID: id.String()}
		}
	}
	return nil
}

// Authenticator turns a raw API key credential into a session.Principal.
type Authenticator struct {
	store   Store
	usage   *UsageWorker
	scheme  Scheme
	members Members
}

// NewAuthenticator binds an Authenticator to its store, usage recorder, key scheme and the membership gate a personal token's owner is checked against on every use. usage may be nil.
func NewAuthenticator(s Store, u *UsageWorker, scheme Scheme, members Members) *Authenticator {
	return &Authenticator{store: s, usage: u, scheme: scheme, members: members}
}

// Scheme returns the key scheme this Authenticator resolves, so a surface gate never restates it.
func (a *Authenticator) Scheme() Scheme { return a.scheme }

var _ authn.Authenticator = (*Authenticator)(nil)

// Authenticate implements authn.Authenticator.
func (a *Authenticator) Authenticate(ctx context.Context, raw string) (session.Principal, error) {
	k, err := a.resolve(ctx, raw)
	if err != nil {
		return session.Principal{}, err
	}
	return principalFor(k), nil
}

// Authorize resolves raw and additionally requires scope on resourceID within orgID/projectID.
func (a *Authenticator) Authorize(ctx context.Context, raw, scope string, orgID, projectID, resourceID uuid.UUID) (session.Principal, error) {
	k, err := a.resolve(ctx, raw)
	if err != nil {
		return session.Principal{}, err
	}
	p := principalFor(k)
	if !p.ReachesProject(orgID, projectID) {
		return session.Principal{}, &authn.UnauthorizedError{}
	}
	if !slices.Contains(p.Scopes, scope) || !p.ReachesResource(orgID, projectID, resourceID) {
		return session.Principal{}, &authn.InsufficientScopeError{Scope: scope}
	}
	return p, nil
}

// AuthorizeProject resolves raw and requires an unrestricted project-wide grant of scope.
func (a *Authenticator) AuthorizeProject(ctx context.Context, raw, scope string, orgID, projectID uuid.UUID) (session.Principal, error) {
	k, err := a.resolve(ctx, raw)
	if err != nil {
		return session.Principal{}, err
	}
	p := principalFor(k)
	if !p.ReachesProject(orgID, projectID) {
		return session.Principal{}, &authn.UnauthorizedError{}
	}
	if !slices.Contains(p.Scopes, scope) || !p.ReachesWholeProject(orgID, projectID) {
		return session.Principal{}, &authn.InsufficientScopeError{Scope: scope}
	}
	return p, nil
}

// SECURITY: every rejection collapses to one opaque *authn.UnauthorizedError; the shape gate short-circuits on purpose because the prefix is public config, and equalizing it would buy a DoS amplifier (see BACKLOG).
func (a *Authenticator) resolve(ctx context.Context, raw string) (*APIKey, error) {
	if a.scheme.Authn().Looks(raw) != authn.ShapeAPIKey {
		return nil, &authn.UnauthorizedError{}
	}
	sum := sha256.Sum256([]byte(raw))
	k, err := a.store.BySecretHash(ctx, sum)
	if err != nil || k == nil || !k.Usable(time.Now().UTC()) {
		return nil, &authn.UnauthorizedError{}
	}
	if err := a.requireLiveOwner(ctx, k); err != nil {
		return nil, &authn.UnauthorizedError{}
	}
	if a.usage != nil {
		a.usage.Record(k.ID, tenant.Context{OrgID: k.OrgID, ProjectID: k.ProjectID}, time.Now().UTC())
	}
	return k, nil
}

// SECURITY: a personal token acts for its owner, so it stops the moment the owner leaves the org or loses the role an org-level scope needs.
func (a *Authenticator) requireLiveOwner(ctx context.Context, k *APIKey) error {
	if k.Kind != KindPersonal {
		return nil
	}
	if a.members == nil || k.CreatedBy == uuid.Nil {
		return &authn.UnauthorizedError{}
	}
	return requireOwner(tenant.WithOrg(ctx, k.OrgID), a.members, k.OrgID, k.CreatedBy, k.Scopes)
}

// SECURITY: a key is not a person, so UserID stays nil and no secret travels with the principal.
func principalFor(k *APIKey) session.Principal {
	return session.Principal{
		UserID:          uuid.Nil,
		KeyID:           k.ID,
		Name:            k.Name,
		Source:          session.SourceAPIKey,
		Scopes:          slices.Clone(k.Scopes),
		ActiveOrgID:     k.OrgID,
		ActiveProjectID: k.ProjectID,
		ProjectIDs:      k.ReachableProjects(),
		AllProjects:     k.AllProjects,
		ResourceIDs:     slices.Clone(k.ResourceIDs),
		IssuedAt:        k.CreatedAt,
	}
}
