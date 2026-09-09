package boot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/auth"
	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/onboard"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/password"
	"altalune.id/opensheet/internal/platform"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/spreadsheet"
	"altalune.id/opensheet/internal/todo"
	"altalune.id/opensheet/internal/user"
)

// Services is every domain store, service and workflow the composition root wires.
type Services struct {
	UserStore        user.Store
	OrgStore         org.Store
	ProjectStore     project.Store
	TodoStore        todo.Store
	InviteStore      invite.Store
	OnboardStore     onboard.Store
	CredentialStore  credential.Store
	SpreadsheetStore spreadsheet.Store
	SheetStore       sheet.Store
	APIKeyStore      apikey.Store

	Auth         *auth.Service
	Users        *user.Service
	Orgs         *org.Service
	Projects     *project.Service
	Todos        *todo.Service
	Invites      *invite.Service
	Onboards     *onboard.Service
	Credentials  *credential.Service
	Spreadsheets *spreadsheet.Service
	Sheets       *sheet.Service
	APIKeys      *apikey.Service

	Onboard *user.OnboardWorkflow
	Read    *sheet.ReadWorkflow
	Write   *sheet.WriteWorkflow
	Connect *credential.ConnectWorkflow

	Authn    authn.Chain
	KeyAuthn *apikey.Authenticator
	KeyUsage *apikey.UsageWorker
}

func buildServices(cfg *config.Config, k *platform.Kernel, caps capabilities.Capabilities,
	stateSecret []byte) (*Services, error) {
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

	orgs := org.NewService(orgStore, caps, log, reporter.Unexpected)
	projects := project.NewService(projectStore, log, reporter.Unexpected)
	todos := todo.NewService(todoStore, log, reporter.Unexpected)
	onboards := onboard.NewService(onboardStore, log, reporter.Unexpected)

	invitesEnabled := cfg.Mode == config.ModeCloud || cfg.OIDC.Issuer != ""

	sendWorkflow := invite.NewSendWorkflow(
		inviteStore,
		mail,
		strings.TrimRight(cfg.HTTP.BaseURL, "/")+cfg.HTTP.BasePath,
		log,
		reporter.Unexpected,
	)
	acceptWorkflow := invite.NewAcceptWorkflow(
		inviteStore,
		userStoreForInvite{store: userStore},
		orgStoreForInvite{store: orgStore},
		log,
		reporter.Unexpected,
	)
	invites := invite.NewService(inviteStore, sendWorkflow, acceptWorkflow, invitesEnabled, log, reporter.Unexpected)

	users := user.NewService(
		userStore,
		user.GenesisConfig{Email: cfg.Genesis.Email, Password: cfg.Genesis.Password},
		log,
		reporter.Unexpected,
		user.WithInviteFinder(invites),
	)

	onboardWorkflow := user.NewOnboardWorkflow(
		userStore,
		orgStoreForOnboard{store: orgStore},
		projectStoreForOnboard{store: projectStore},
		inviteStoreForOnboard{store: inviteStore},
		onboardPolicyFrom(cfg),
		log,
		reporter.Unexpected,
	)

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
	ensureFromOIDC := func(ctx context.Context, claims auth.EnsureClaims) (*auth.UserRef, bool, error) {
		u, err := users.EnsureFromOIDC(ctx, user.Claims(claims))
		if err != nil {
			return nil, false, err
		}
		return &auth.UserRef{ID: u.ID, Email: u.Email, Name: u.Name, Source: u.Source, IsAdmin: u.IsAdmin, Locale: u.Locale, TermsAcceptedAt: u.TermsAcceptedAt}, false, nil
	}
	oidcLogin := auth.NewOIDCLogin(
		ensureFromOIDC,
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

	credentialStore := credential.NewStore(cfg.DB, pool, pgConn)
	spreadsheetStore := spreadsheet.NewStore(cfg.DB, pool, pgConn)
	sheetStore := sheet.NewStore(cfg.DB, pool, pgConn)
	sheetAttempts := sheet.NewIdempotencyStore(cfg.DB, pgConn)
	apiKeyStore := apikey.NewStore(cfg.DB, pool, pgConn)

	snaps, err := buildSnapshotStore(cfg, k)
	if err != nil {
		return nil, err
	}

	clients := gsheetFactory(cfg)
	writers := gsheetWriterFactory(cfg)
	connector, oauthCfg := buildGoogleConnector(cfg, log)
	var credOpts []credential.ServiceOption
	if oauthCfg != nil {
		credOpts = append(credOpts, credential.WithGoogleOAuth(oauthCfg))
	}

	sheetCaps := capsForSheet{caps: caps}
	credentials := credential.NewService(credentialStore, log, reporter.Unexpected, k.Sealer, credOpts...)
	spreadsheets := spreadsheet.NewService(spreadsheetStore, log, reporter.Unexpected,
		tokensForSpreadsheetRead{svc: credentials}, clients,
		tokensForSpreadsheetWrite{svc: credentials}, writers)
	sheets := sheet.NewService(sheetStore, log, reporter.Unexpected, sheetCaps, snaps, sheetAttempts)
	apiKeys := apikey.NewService(apiKeyStore, log, reporter.Unexpected, sheetStoreForAPIKey{store: sheetStore})

	readWorkflow := sheet.NewReadWorkflow(
		snaps,
		spreadsheetsForSheet{svc: spreadsheets},
		tokensForSheetRead{svc: credentials},
		credentialsForSheet{svc: credentials},
		clients,
		sheetCaps,
		cfg.Cache.DefaultTTL,
		cfg.Sheets.MaxPayloadBytes,
		log,
		reporter.Unexpected,
	)
	writeWorkflow := sheet.NewWriteWorkflow(
		snaps,
		sheetAttempts,
		spreadsheetsForSheet{svc: spreadsheets},
		tokensForSheetWrite{svc: credentials},
		credentialsForSheet{svc: credentials},
		writers,
		log,
		reporter.Unexpected,
	)
	connectWorkflow := credential.NewConnectWorkflow(
		credentialStore,
		k.Sealer,
		connector,
		stateSecret,
		nil,
		log,
		reporter.Unexpected,
	)

	keyUsage := apikey.NewUsageWorker(apiKeyStore, log, nil)
	keyAuthn := apikey.NewAuthenticator(apiKeys, keyUsage)
	chain := authn.Chain{
		keyAuthn,
		auth.NewTokenLogin(k.Verifier, ensureFromOIDC, membershipsFor(orgs), log, reporter.Unexpected),
	}

	svcs := &Services{
		UserStore:        userStore,
		OrgStore:         orgStore,
		ProjectStore:     projectStore,
		TodoStore:        todoStore,
		InviteStore:      inviteStore,
		OnboardStore:     onboardStore,
		CredentialStore:  credentialStore,
		SpreadsheetStore: spreadsheetStore,
		SheetStore:       sheetStore,
		APIKeyStore:      apiKeyStore,
		Auth:             auths,
		Users:            users,
		Orgs:             orgs,
		Projects:         projects,
		Todos:            todos,
		Invites:          invites,
		Onboards:         onboards,
		Credentials:      credentials,
		Spreadsheets:     spreadsheets,
		Sheets:           sheets,
		APIKeys:          apiKeys,
		Onboard:          onboardWorkflow,
		Read:             readWorkflow,
		Write:            writeWorkflow,
		Connect:          connectWorkflow,
		Authn:            chain,
		KeyAuthn:         keyAuthn,
		KeyUsage:         keyUsage,
	}
	if wErr := assertServicesWiring(svcs); wErr != nil {
		return nil, wErr
	}
	return svcs, nil
}

// authnChainLinks names each slot in the assembled authn.Chain, in order.
//
//nolint:gochecknoglobals // Immutable wiring manifest; not runtime state.
var authnChainLinks = []string{"apikey", "token"}

func assertServicesWiring(s *Services) error {
	slots := []struct {
		name  string
		wired bool
	}{
		{"credentials", s.Credentials != nil},
		{"spreadsheets", s.Spreadsheets != nil},
		{"sheets", s.Sheets != nil},
		{"apikeys", s.APIKeys != nil},
		{"read", s.Read != nil},
		{"write", s.Write != nil},
		{"connect", s.Connect != nil},
		{"keyAuthn", s.KeyAuthn != nil},
		{"keyUsage", s.KeyUsage != nil},
	}
	missing := make([]string, 0, len(slots))
	for _, slot := range slots {
		if !slot.wired {
			missing = append(missing, slot.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("boot: service wiring: slots missing a service: %v", missing)
	}
	if len(s.Authn) != len(authnChainLinks) {
		return fmt.Errorf("boot: authn wiring: %d authenticators for %d links %v",
			len(s.Authn), len(authnChainLinks), authnChainLinks)
	}
	for i, a := range s.Authn {
		if a == nil {
			return fmt.Errorf("boot: authn wiring: link %q is nil", authnChainLinks[i])
		}
	}
	return nil
}

func gsheetFactory(cfg *config.Config) gsheet.Factory {
	return func(ctx context.Context, ts oauth2.TokenSource) (*gsheet.Client, error) {
		return gsheet.New(ctx, ts, gworkspace.WithTimeout(cfg.Google.Timeout))
	}
}

func gsheetWriterFactory(cfg *config.Config) gsheet.WriterFactory {
	return func(ctx context.Context, ts oauth2.TokenSource) (*gsheet.Writer, error) {
		return gsheet.NewWriter(ctx, ts, gworkspace.WithTimeout(cfg.Google.Timeout))
	}
}

func buildGoogleConnector(cfg *config.Config, log *slog.Logger) (*gworkspace.Connector, *oauth2.Config) {
	if strings.TrimSpace(cfg.Google.OAuth.ClientID) == "" {
		return nil, nil
	}
	oauthCfg := &oauth2.Config{
		ClientID:     cfg.Google.OAuth.ClientID,
		ClientSecret: cfg.Google.OAuth.ClientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  googleRedirectURL(cfg),
		Scopes:       gworkspace.Scopes(),
	}
	connector, err := gworkspace.NewConnector(oauthCfg, gworkspace.Scopes(), gworkspace.WithTimeout(cfg.Google.Timeout))
	if err != nil {
		// NOTE: a selfhosted deployment legitimately has no Google client, so an unusable one turns the capability off instead of failing the boot.
		log.Warn("boot: google oauth client unusable - the connect flow is disabled", slog.String("err", err.Error()))
		return nil, nil
	}
	return connector, oauthCfg
}

// NOTE: Google requires an exact-match redirect URI, so this is one fixed path per deployment; org, project and user travel in the signed state.
func googleRedirectURL(cfg *config.Config) string {
	if cfg.HTTP.BaseURL == "" {
		return ""
	}
	return strings.TrimRight(cfg.HTTP.BaseURL, "/") + cfg.HTTP.BasePath + "/credentials/google/callback"
}

func onboardPolicyFrom(cfg *config.Config) user.Policy {
	policyMode := user.PolicyModeCloud
	if cfg.Mode == config.ModeSelfhosted {
		policyMode = user.PolicyModeSelfhosted
	}
	return user.Policy{
		Mode:             policyMode,
		SingletonOrgSlug: cfg.Tenant.SingletonOrg.Slug,
	}
}

func hashGenesisPassword(plain string) (string, error) {
	if strings.TrimSpace(plain) == "" {
		return "", nil
	}
	return password.Hash(plain)
}
