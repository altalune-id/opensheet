package boot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/auth"
	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/onboard"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/password"
	"altalune.id/opensheet/internal/platform"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/platform/tokens"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/role"
	"altalune.id/opensheet/internal/todo"
	"altalune.id/opensheet/internal/user"
	"altalune.id/opensheet/internal/webhook"
)

const apiKeyUsageFlushInterval = 30 * time.Second

// Services is every domain store, service and workflow the composition root wires.
type Services struct {
	UserStore    user.Store
	OrgStore     org.Store
	ProjectStore project.Store
	TodoStore    todo.Store
	InviteStore  invite.Store
	OnboardStore onboard.Store
	WebhookStore webhook.Store
	RoleStore    role.Store

	Auth     *auth.Service
	Users    *user.Service
	Orgs     *org.Service
	Projects *project.Service
	Todos    *todo.Service
	Invites  *invite.Service
	Onboards *onboard.Service
	Webhooks *webhook.Service
	Roles    *role.Service

	Onboard *user.OnboardWorkflow

	Authn       authn.Chain
	KeyAuthn    *apikey.Authenticator
	APIKeys     *apikey.Service
	APIKeyUsage *apikey.UsageWorker

	opensheetServices
}

func buildServices(cfg *config.Config, k *platform.Kernel, caps capabilities.Capabilities) (*Services, error) {
	pool := k.Pool
	pgConn := k.PgConn
	log := k.Log
	reporter := k.Reporter
	mail := k.Mail

	userStore := user.NewStore(cfg.DB, pool)
	orgStore := org.NewStore(cfg.DB, pool, pgConn)
	projectStore := project.NewStore(cfg.DB, pool, pgConn)
	todoStore := todo.NewStore(cfg.DB, pool, pgConn)
	inviteStore := invite.NewStore(cfg.DB, pool, pgConn)
	onboardStore := onboard.NewStore(cfg.DB, pool)
	roleStore := role.NewStore(cfg.DB, pool, pgConn)
	uow := tenant.NewUnitOfWork(cfg.DB, pool, pgConn)
	resolver := newAuthzResolver(orgStore, roleStore)

	orgs := org.NewService(orgStore, caps, log, reporter.Unexpected, uow, org.WithPermissions(resolver))
	roles := newRoleService(cfg, roleStore, orgStore, log, reporter.Unexpected, uow)
	projects := project.NewService(projectStore, log, reporter.Unexpected)
	todos := todo.NewService(todoStore, log, reporter.Unexpected, k.Queue)
	onboards := onboard.NewService(onboardStore, log, reporter.Unexpected)
	webhookStore := webhook.NewStore(cfg.DB, pool, pgConn)
	webhooks := webhook.NewService(webhookStore, log, reporter.Unexpected, k.Sealer, k.Outbox, projectSlugs{svc: projects})

	invitesEnabled := cfg.Mode == config.ModeCloud || cfg.OIDC.Issuer != ""

	sendWorkflow := invite.NewSendWorkflow(
		inviteStore,
		mail,
		strings.TrimRight(cfg.HTTP.BaseURL, "/")+cfg.HTTP.BasePath,
		log,
		reporter.Unexpected,
	)
	acceptWorkflow := newAcceptWorkflow(inviteStore, userStore, orgStore, uow, log, reporter.Unexpected)
	invites := invite.NewService(inviteStore, sendWorkflow, acceptWorkflow, invitesEnabled, log, reporter.Unexpected, invite.WithPermissions(resolver))

	users := user.NewService(
		userStore,
		user.GenesisConfig{Email: cfg.Genesis.Email, Password: cfg.Genesis.Password},
		log,
		reporter.Unexpected,
		user.WithInviteFinder(invites),
	)

	onboardWorkflow := newOnboardWorkflow(cfg, userStore, orgStore, projectStore, inviteStore, uow, log, reporter.Unexpected)

	genesisHash, err := hashGenesisPassword(cfg.Genesis.Password)
	if err != nil {
		return nil, fmt.Errorf("boot: hash genesis password: %w", err)
	}
	local := auth.NewLocalLogin(
		userStoreForAuth{store: userStore},
		auth.Genesis{
			Email:        cfg.Genesis.Email,
			PasswordHash: genesisHash,
			Name:         cfg.Genesis.Email,
		},
		log,
		reporter.Unexpected,
		auth.WithLocalNotFound(user.IsNotFoundError),
	)

	oidcOpts := []auth.OIDCOption{
		auth.WithSignupRequired(user.IsSignupRequiredError),
	}
	if cfg.Mode == config.ModeSelfhosted {
		oidcOpts = append(oidcOpts, auth.WithAllowSignup(func(ctx context.Context, email string) error {
			if req, rerr := onboards.Required(ctx); rerr == nil && req {
				return nil
			}
			return users.CheckOIDCSignupEligibility(ctx, email)
		}))
	}
	oidcLogin := auth.NewOIDCLogin(
		func(ctx context.Context, claims auth.EnsureClaims) (*auth.UserRef, bool, error) {
			u, err := users.EnsureFromOIDC(ctx, user.Claims(claims))
			if err != nil {
				return nil, false, err
			}
			return &auth.UserRef{ID: u.ID, Email: u.Email, Name: u.Name, Source: u.Source, IsAdmin: u.IsAdmin, Locale: u.Locale, TermsAcceptedAt: u.TermsAcceptedAt}, false, nil
		},
		func(ctx context.Context, req auth.OnboardRequest) (auth.OnboardResult, error) {
			res, err := onboardWorkflow.Onboard(ctx, req.UserID, req.Email)
			if err != nil {
				return auth.OnboardResult{}, err
			}
			return auth.OnboardResult{OrgID: res.OrgID, ProjectID: res.ProjectID}, nil
		},
		log,
		reporter.Unexpected,
		oidcOpts...,
	)

	auths := auth.NewService(local, oidcLogin, log, reporter.Unexpected)

	keyStore := apikey.NewStore(cfg.DB, pool, pgConn)
	keyUsage := apikey.NewUsageWorker(keyStore, apiKeyUsageFlushInterval, log)
	keyScheme := apikey.NewScheme(cfg.API.KeyPrefix)
	keyAuthn := apikey.NewAuthenticator(keyStore, keyUsage, keyScheme)
	keys := apikey.NewService(keyStore, keyScheme, orgs, projectServiceForAPIKeys{svc: projects}, log, reporter.Unexpected)
	orgs.OnMemberRemoved(keys.RevokePersonalOf)
	// SECURITY: key first, so its shape gate rejects a non-key credential without a DB call.
	tenantResolution := user.WithTenantResolution(
		orgStoreForOnboard{store: orgStore},
		projectStoreForOnboard{store: projectStore},
	)
	tokenAuthn := tokens.NewAuthenticator(k.Verifier, credentialScheme(keyScheme, cfg.Tokens.Introspection))
	chain := authn.Chain{keyAuthn, user.NewAuthenticator(tokenAuthn, userStore, tenantResolution)}

	return &Services{
		UserStore:    userStore,
		OrgStore:     orgStore,
		ProjectStore: projectStore,
		TodoStore:    todoStore,
		InviteStore:  inviteStore,
		OnboardStore: onboardStore,
		WebhookStore: webhookStore,
		RoleStore:    roleStore,
		Auth:         auths,
		Users:        users,
		Orgs:         orgs,
		Projects:     projects,
		Todos:        todos,
		Invites:      invites,
		Onboards:     onboards,
		Webhooks:     webhooks,
		Roles:        roles,
		Onboard:      onboardWorkflow,
		Authn:        chain,
		KeyAuthn:     keyAuthn,
		APIKeys:      keys,
		APIKeyUsage:  keyUsage,

		opensheetServices: opensheetServices{Authz: resolver},
	}, nil
}

func newRoleService(cfg *config.Config, roles role.Store, orgs org.Store, log *slog.Logger, unexpected apperror.UnexpectedFunc, uow tenant.UnitOfWork) *role.Service {
	return role.NewService(roles, orgStaffForRoles{store: orgs, cloud: cfg.Mode == config.ModeCloud}, log, unexpected, uow)
}

func newAcceptWorkflow(invites invite.Store, users user.Store, orgs org.Store, uow tenant.UnitOfWork, log *slog.Logger, unexpected apperror.UnexpectedFunc) *invite.AcceptWorkflow {
	return invite.NewAcceptWorkflow(invites, userStoreForInvite{store: users}, orgStoreForInvite{store: orgs}, log, unexpected, invite.WithUnitOfWork(uow))
}

func newOnboardWorkflow(cfg *config.Config, users user.Store, orgs org.Store, projects project.Store, invites invite.Store, uow tenant.UnitOfWork, log *slog.Logger, unexpected apperror.UnexpectedFunc) *user.OnboardWorkflow {
	return user.NewOnboardWorkflow(users, orgStoreForOnboard{store: orgs}, projectStoreForOnboard{store: projects},
		inviteStoreForOnboard{store: invites}, onboardPolicyFrom(cfg), log, unexpected, user.WithOnboardUnitOfWork(uow))
}

func onboardPolicyFrom(cfg *config.Config) user.Policy {
	policyMode := user.PolicyModeCloud
	if cfg.Mode == config.ModeSelfhosted {
		policyMode = user.PolicyModeSelfhosted
	}
	return user.Policy{Mode: policyMode}
}

func hashGenesisPassword(plain string) (string, error) {
	if strings.TrimSpace(plain) == "" {
		return "", nil
	}
	return password.Hash(plain)
}
