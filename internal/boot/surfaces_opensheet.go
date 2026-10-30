package boot

import (
	"log/slog"
	"slices"

	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/queue"
	"altalune.id/opensheet/internal/published"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/todo"
	"altalune.id/opensheet/internal/web"
	webhandlers "altalune.id/opensheet/internal/web/handlers"
	"altalune.id/opensheet/scheduler"
)

const (
	mountTodo            = published.Todo
	mountWebhooksConsole = published.WebhooksConsole
)

// https://developers.google.com/identity/protocols/oauth2/web-server
const googleConsentOrigin = "https://accounts.google.com"

func opensheetFormAction(cfg *config.Config) []string {
	if cfg.Google.OAuth.ClientID == "" {
		return nil
	}
	return []string{googleConsentOrigin}
}

func publishedMCPDomains() []string { return []string{"org.v1", "project.v1"} }

func publishedConsoleHandlers(all []web.Register) []web.Register {
	return slices.DeleteFunc(slices.Clone(all), func(h web.Register) bool {
		switch h.(type) {
		case *webhandlers.TodoHandler:
			return !mountTodo
		case *webhandlers.WebhookHandler:
			return !mountWebhooksConsole
		}
		return false
	})
}

func opensheetConsoleHandlers(deps webhandlers.Deps, s *Services) []web.Register {
	hs := []web.Register{
		webhandlers.NewCredentialHandler(deps, s.Projects, s.Credentials, s.Connect),
		webhandlers.NewSpreadsheetHandler(deps, s.Projects, s.Spreadsheets, s.Credentials),
		webhandlers.NewSheetHandler(deps, s.Projects, s.Sheets, s.Spreadsheets, s.Read, s.Fix),
		webhandlers.NewGoogleConnectHandler(deps, s.Projects, s.Credentials, s.Connect),
	}
	if !mountTodo {
		hs = append(hs, webhandlers.NewOpensheetOverviewHandler(deps, s.Projects, s.Credentials, s.Spreadsheets, s.Sheets))
	}
	return hs
}

func opensheetListenerProviders(s *Services) []queue.ListenerProvider {
	return []queue.ListenerProvider{s.SnapshotListener}
}

func publishedConsumers(all []queue.Provider) []queue.Provider {
	return slices.DeleteFunc(slices.Clone(all), func(p queue.Provider) bool {
		_, isTodo := p.(*todo.Consumer)
		return isTodo && !mountTodo
	})
}

func publishedSchedulers(all []scheduler.Provider) []scheduler.Provider {
	return slices.DeleteFunc(slices.Clone(all), func(p scheduler.Provider) bool {
		_, isTodo := p.(*todo.Scheduler)
		return isTodo && !mountTodo
	})
}

func opensheetSchedulerDomains() []string { return []string{"sheet"} }

func opensheetSchedulerProviders(s *Services, log *slog.Logger) []scheduler.Provider {
	return []scheduler.Provider{sheet.NewScheduler(s.Sheets, log)}
}
