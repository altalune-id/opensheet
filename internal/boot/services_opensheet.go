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
	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/platform"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/spreadsheet"
)

type opensheetServices struct {
	CredentialStore  credential.Store
	SpreadsheetStore spreadsheet.Store
	SheetStore       sheet.Store

	Credentials  *credential.Service
	Spreadsheets *spreadsheet.Service
	Sheets       *sheet.Service

	Read    *sheet.ReadWorkflow
	Write   *sheet.WriteWorkflow
	Fix     *sheet.FixWorkflow
	Connect *credential.ConnectWorkflow

	SnapshotListener *sheet.SnapshotListener
}

func buildOpensheetServices(cfg *config.Config, k *platform.Kernel, caps capabilities.Capabilities) (opensheetServices, error) {
	pool := k.Pool
	pgConn := k.PgConn
	log := k.Log
	reporter := k.Reporter

	credentialStore := credential.NewStore(cfg.DB, pool, pgConn)
	spreadsheetStore := spreadsheet.NewStore(cfg.DB, pool, pgConn)
	sheetStore := sheet.NewStore(cfg.DB, pool, pgConn)
	sheetRows := sheet.NewRowStore(cfg.DB, pool, pgConn)
	sheetUnits := sheet.NewUnitOfWork(cfg.DB, pool, pgConn)
	sheetAttempts := sheet.NewIdempotencyStore(cfg.DB, pgConn)

	snaps, err := buildSnapshotStore(cfg, k)
	if err != nil {
		return opensheetServices{}, err
	}

	clients := gsheetFactory(cfg)
	writers := gsheetWriterFactory(cfg)
	connector, oauthCfg := buildGoogleConnector(cfg, log)
	var credOpts []credential.ServiceOption
	if oauthCfg != nil {
		credOpts = append(credOpts, credential.WithGoogleOAuth(oauthCfg))
	}

	sheetCaps := capsForSheet{caps: caps}
	credSealer := credentialSealer(cfg, k)
	credentials := credential.NewService(credentialStore, log, reporter.Unexpected, credSealer, credOpts...)
	spreadsheets := spreadsheet.NewService(spreadsheetStore, log, reporter.Unexpected,
		tokensForSpreadsheetRead{svc: credentials}, clients,
		tokensForSpreadsheetWrite{svc: credentials}, writers)
	publishWorkflow := sheet.NewPublishWorkflow(
		spreadsheetsForSheet{svc: spreadsheets},
		tokensForSheetRead{svc: credentials},
		credentialsForSheet{svc: credentials},
		clients,
		log,
		reporter.Unexpected,
	)
	sheets := sheet.NewService(sheetStore, log, reporter.Unexpected, sheetCaps, snaps, sheetAttempts, publishWorkflow,
		sheet.WithQueue(k.Queue))

	readWorkflow := sheet.NewReadWorkflow(
		snaps,
		sheetRows,
		spreadsheetsForSheet{svc: spreadsheets},
		tokensForSheetRead{svc: credentials},
		credentialsForSheet{svc: credentials},
		clients,
		sheetCaps,
		cfg.Cache.DefaultTTL,
		cfg.Sheets.MaxPayloadBytes,
		cfg.Sheets.MaxQueryRows,
		cfg.Sheets.MaxSortPages,
		log,
		reporter.Unexpected,
	)
	writeWorkflow := sheet.NewWriteWorkflow(
		snaps,
		sheetRows,
		sheetUnits,
		sheetAttempts,
		spreadsheetsForSheet{svc: spreadsheets},
		tokensForSheetWrite{svc: credentials},
		credentialsForSheet{svc: credentials},
		writers,
		cfg.Cache.DefaultTTL,
		cfg.Sheets.MaxPayloadBytes,
		log,
		reporter.Unexpected,
		sheet.WithQueue(k.Queue),
	)
	fixWorkflow := sheet.NewFixWorkflow(
		spreadsheetsForSheet{svc: spreadsheets},
		tokensForSheetWrite{svc: credentials},
		credentialsForSheet{svc: credentials},
		writers,
		log,
		reporter.Unexpected,
	)
	connectWorkflow := credential.NewConnectWorkflow(
		credentialStore,
		credSealer,
		connector,
		k.OIDCStateSecret,
		nil,
		log,
		reporter.Unexpected,
	)

	s := opensheetServices{
		CredentialStore:  credentialStore,
		SpreadsheetStore: spreadsheetStore,
		SheetStore:       sheetStore,
		Credentials:      credentials,
		Spreadsheets:     spreadsheets,
		Sheets:           sheets,
		Read:             readWorkflow,
		Write:            writeWorkflow,
		Fix:              fixWorkflow,
		Connect:          connectWorkflow,
		SnapshotListener: sheet.NewSnapshotListener(snaps),
	}
	if err := assertOpensheetWiring(s); err != nil {
		return opensheetServices{}, err
	}
	return s, nil
}

// SECURITY: a stored Google credential outlives the process, so an ephemeral key (kept by the template for sessions) would seal it unreadable after a restart; credentials fail closed instead.
func credentialSealer(cfg *config.Config, k *platform.Kernel) sealer.Sealer {
	if strings.TrimSpace(cfg.Security.EncryptionKey) == "" {
		k.Log.Warn("boot: security.encryptionKey is empty - credential storage is disabled")
		return sealer.Disabled()
	}
	return k.Sealer
}

func assertOpensheetWiring(s opensheetServices) error {
	slots := []struct {
		name  string
		wired bool
	}{
		{"credentials", s.Credentials != nil},
		{"spreadsheets", s.Spreadsheets != nil},
		{"sheets", s.Sheets != nil},
		{"read", s.Read != nil},
		{"write", s.Write != nil},
		{"fix", s.Fix != nil},
		{"connect", s.Connect != nil},
		{"snapshot listener", s.SnapshotListener != nil},
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
