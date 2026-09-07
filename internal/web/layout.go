package web

import (
	"os"
	"strings"

	"altalune.id/template/internal/i18n"
	"altalune.id/template/internal/platform/capabilities"
	"altalune.id/template/internal/platform/session"

	"github.com/a-h/templ"
)

// UIMode names the asset delivery strategy.
type UIMode string

const (
	// UIModeCDN pulls Tailwind/Basecoat/HTMX from public CDNs.
	UIModeCDN UIMode = "cdn"
	// UIModeVendored serves them from the embedded /static/* mount.
	UIModeVendored UIMode = "vendored"
)

// ResolveUIMode reads ALT_UI_MODE; "vendored" flips to vendored, otherwise CDN.
func ResolveUIMode() UIMode {
	if os.Getenv("ALT_UI_MODE") == string(UIModeVendored) {
		return UIModeVendored
	}
	return UIModeCDN
}

// NavScope selects which sidebars the shell renders.
type NavScope string

const (
	// NavScopeNone hides both sidebars (login, onboarding, error).
	NavScopeNone NavScope = ""
	// NavScopeOrg shows sidebar A only.
	NavScopeOrg NavScope = "org"
	// NavScopeProject shows sidebar A and sidebar B.
	NavScopeProject NavScope = "project"
)

// ActiveNav tells the layout which sidebar item to mark selected.
type ActiveNav struct {
	Scope      NavScope
	OrgKey     string
	ProjectKey string
}

// ActiveOrg is what the org switcher pill and menu heading show.
type ActiveOrg struct {
	ID   string
	Slug string
	Name string
	Role string
}

// ActiveProject is what the project switcher pill and sidebar heading show.
type ActiveProject struct {
	ID   string
	Slug string
	Name string
}

// LayoutData is the shape every page-level templ component receives.
type LayoutData struct {
	Title         string
	BasePath      string
	BaseURL       string
	Version       string
	UIMode        UIMode
	Caps          capabilities.Capabilities
	Principal     *session.Principal
	Flash         *FlashMessage
	Content       templ.Component
	ActiveOrg     *ActiveOrg
	OtherOrgs     []ActiveOrg
	ActiveProject *ActiveProject
	OtherProjects []ActiveProject
	ActiveNav     ActiveNav
	Locale        i18n.Locale
	Dir           string
	Translator    *i18n.Translator
	CurrentPath   string
	SupportedLocs []i18n.Locale
	Themes        []Theme
	ColorModes    []ColorMode
	// RequestID is echoed on every user-visible error so a report can be matched to a log line.
	RequestID string
}

// LocaleOption is one row in the locale-selector dropdown.
type LocaleOption struct {
	Code   string
	Native string
	Active bool
}

// Tr returns the translation for key. Args are key/value pairs.
func (d LayoutData) Tr(key string, args ...any) string {
	if d.Translator == nil {
		return key
	}
	return d.Translator.T(key, args...)
}

// TrN returns the pluralized translation with Count auto-injected. Extra args are key/value pairs.
func (d LayoutData) TrN(key string, n int, args ...any) string {
	if d.Translator == nil {
		return key
	}
	return d.Translator.Tn(key, n, args...)
}

// LocaleLabel returns the native name for the active locale.
func (d LayoutData) LocaleLabel() string {
	if d.Locale == "" {
		return i18n.EnUS.NativeName()
	}
	return d.Locale.NativeName()
}

// LocaleOptions returns every supported locale ordered for the dropdown.
func (d LayoutData) LocaleOptions() []LocaleOption {
	out := make([]LocaleOption, 0, len(d.SupportedLocs))
	for _, l := range d.SupportedLocs {
		out = append(out, LocaleOption{
			Code:   string(l),
			Native: l.NativeName(),
			Active: l == d.Locale,
		})
	}
	return out
}

// FlashMessage is the small "we saved that / that failed" banner rendered above content.
type FlashMessage struct {
	Kind    string
	Message string
}

// Static returns a basePath-aware URL for a vendored asset (e.g. static/htmx.min.js).
func (d LayoutData) Static(sub string) string { return Path(d.BasePath, "static/"+sub) }

// Href joins BasePath with a subpath. Templates use this so mounts under /app work.
func (d LayoutData) Href(sub string) string { return Path(d.BasePath, sub) }

// OrgPath returns sub under the active org, so the nested URL shape lives in one place.
func (d LayoutData) OrgPath(sub string) string {
	if d.ActiveOrg == nil {
		return d.Href("/orgs")
	}
	return d.Href("/orgs/" + d.ActiveOrg.Slug + sub)
}

// ProjectPath returns sub under one of the active org's projects.
func (d LayoutData) ProjectPath(projectSlug, sub string) string {
	return d.OrgPath("/projects/" + projectSlug + sub)
}

// OIDCButtonLabel returns cfg.OIDC.ButtonLabel if set, else fallback.
func (d LayoutData) OIDCButtonLabel(fallback string) string {
	if s := strings.TrimSpace(d.Caps.OIDCButtonLabel); s != "" {
		return s
	}
	return fallback
}

// OIDCButtonLogoURL returns cfg.OIDC.ButtonLogoURL if set, else the vendored altalune-logo.png under BasePath.
func (d LayoutData) OIDCButtonLogoURL() string {
	if s := strings.TrimSpace(d.Caps.OIDCButtonLogoURL); s != "" {
		return s
	}
	return d.Static("altalune-logo.png")
}

// WithContent returns a copy of d with Content set.
func WithContent(d LayoutData, c templ.Component) LayoutData {
	d.Content = c
	return d
}
