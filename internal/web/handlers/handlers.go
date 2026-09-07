// Package handlers hosts the HTTP handlers behind the templ-rendered pages.
package handlers

import (
	"cmp"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/google/uuid"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/i18n"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/capabilities"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/web"
	"altalune.id/template/internal/web/templates"
	"altalune.id/template/reqid"
	"altalune.id/template/version"
)

// Deps is the common bag of dependencies every handler in this package needs.
type Deps struct {
	Cfg      *config.Config
	Caps     capabilities.Capabilities
	Sessions session.Store
	Logger   *log.Logger
	Orgs     *org.Service
	Projects *project.Service
	I18n     *i18n.Bundle
}

// Base builds a minimal LayoutData for chromeless pages (login, onboarding, error).
func (d Deps) Base(r *http.Request, title string) web.LayoutData {
	p := session.PrincipalFrom(r.Context())
	var pp *session.Principal
	if p.UserID != uuid.Nil {
		pp = &p
	}
	loc := i18n.From(r.Context())
	dir := loc.Dir()
	var supported []i18n.Locale
	if d.I18n != nil {
		dir = d.I18n.Dir(loc)
		supported = d.I18n.All()
	}
	return web.LayoutData{
		Title:         title,
		BasePath:      d.Cfg.HTTP.BasePath,
		BaseURL:       d.Cfg.HTTP.BaseURL,
		Version:       version.Default(),
		UIMode:        web.ResolveUIMode(),
		Caps:          d.Caps,
		Principal:     pp,
		Locale:        loc,
		Dir:           dir,
		Translator:    i18n.TranslatorFrom(r.Context()),
		CurrentPath:   r.URL.RequestURI(),
		SupportedLocs: supported,
		Themes:        web.Themes(),
		ColorModes:    web.ColorModes(),
		RequestID:     reqid.FromContext(r.Context()),
	}
}

// Layout builds a LayoutData for a signed-in page and populates org/project switchers.
func (d Deps) Layout(r *http.Request, title string, nav web.ActiveNav) web.LayoutData {
	return d.layout(r, title, nav, uuid.Nil, uuid.Nil)
}

// layout builds a LayoutData for a signed-in page. Both switchers are keyed on pinnedOrg — the org named
// by the path — falling back to the session's last-used org only on pages that name none.
// NOTE: the pinned org must be known before the list is split, or it lands in both Current and Switch.
func (d Deps) layout(r *http.Request, title string, nav web.ActiveNav, pinnedOrg, pinnedProject uuid.UUID) web.LayoutData {
	base := d.Base(r, title)
	if base.Principal == nil {
		return base
	}
	base.ActiveNav = nav
	ctx := r.Context()
	p := *base.Principal
	if d.Orgs == nil {
		return base
	}

	orgs, err := d.Orgs.List(ctx, p.UserID)
	if err != nil {
		d.LogErr("layout: list orgs", err)
	}
	base.ActiveOrg, base.OtherOrgs = splitOrgs(orgs, cmp.Or(pinnedOrg, p.ActiveOrgID))
	if base.ActiveOrg == nil {
		return base
	}

	orgID := uuidFromString(base.ActiveOrg.ID)
	if m, mErr := d.Orgs.MembershipOf(ctx, orgID, p.UserID); mErr == nil && m != nil {
		base.ActiveOrg.Role = capitaliseRole(string(m.Role))
	}
	if d.Projects == nil {
		return base
	}
	// SECURITY: the project switcher lists the pinned org's projects, so a stale session cannot leak another org's names.
	tctx := tenant.Into(ctx, tenant.Context{OrgID: orgID, UserID: p.UserID, ProjectID: pinnedProject})
	projects, pErr := d.Projects.List(tctx, orgID)
	if pErr != nil {
		d.LogErr("layout: list projects", pErr)
	}
	base.ActiveProject, base.OtherProjects = splitProjects(projects, cmp.Or(pinnedProject, p.ActiveProjectID))
	return base
}

// LayoutForOrg tags a page as org-scoped and pins both switchers to the org named by the path.
func (d Deps) LayoutForOrg(r *http.Request, title, slug, orgKey string) web.LayoutData {
	return d.layout(r, title, web.ActiveNav{Scope: web.NavScopeOrg, OrgKey: orgKey}, d.orgIDForSlug(r, slug), uuid.Nil)
}

func (d Deps) orgIDForSlug(r *http.Request, slug string) uuid.UUID {
	if slug == "" || d.Orgs == nil {
		return uuid.Nil
	}
	o, err := d.Orgs.BySlug(r.Context(), slug)
	if err != nil || o == nil {
		return uuid.Nil
	}
	return o.ID
}

// LayoutForProject tags a page as project-scoped and pins both switchers to the org and project the path names.
func (d Deps) LayoutForProject(r *http.Request, title, orgSlug string, proj *project.Project, projectKey string) web.LayoutData {
	nav := web.ActiveNav{Scope: web.NavScopeProject, ProjectKey: projectKey}
	l := d.layout(r, title, nav, d.orgIDForSlug(r, orgSlug), proj.ID)
	if l.ActiveProject == nil || l.ActiveProject.Slug != proj.Slug {
		l.ActiveProject = &web.ActiveProject{ID: proj.ID.String(), Slug: proj.Slug, Name: proj.Name}
	}
	return l
}

// ProjectScopeFor resolves the project named by slug inside orgID and returns a request scoped to both.
// SECURITY: r must already carry the org scope from OrgScopeFor, whose membership check gates this lookup.
func (d Deps) ProjectScopeFor(w http.ResponseWriter, r *http.Request, orgID uuid.UUID, slug string) (*project.Project, *http.Request, bool) {
	proj, err := d.Projects.BySlug(r.Context(), orgID, slug)
	if err != nil {
		d.ErrorPage(w, r, http.StatusNotFound, "Project not found", "No project with that slug in this organization.", err)
		return nil, nil, false
	}
	return proj, r.WithContext(tenant.WithProject(r.Context(), proj.ID)), true
}

func splitOrgs(items []*org.Org, activeID uuid.UUID) (active *web.ActiveOrg, others []web.ActiveOrg) { //nolint:nonamedreturns // two return values differ in role
	others = make([]web.ActiveOrg, 0, len(items))
	for _, o := range items {
		summary := web.ActiveOrg{ID: o.ID.String(), Slug: o.Slug, Name: o.Name}
		if o.ID == activeID {
			a := summary
			active = &a
			continue
		}
		others = append(others, summary)
	}
	if active == nil && len(items) > 0 {
		a := web.ActiveOrg{ID: items[0].ID.String(), Slug: items[0].Slug, Name: items[0].Name}
		active = &a
		others = others[:0]
		for _, o := range items[1:] {
			others = append(others, web.ActiveOrg{ID: o.ID.String(), Slug: o.Slug, Name: o.Name})
		}
	}
	return active, others
}

func splitProjects(items []*project.Project, activeID uuid.UUID) (active *web.ActiveProject, others []web.ActiveProject) { //nolint:nonamedreturns // two return values differ in role
	others = make([]web.ActiveProject, 0, len(items))
	for _, p := range items {
		summary := web.ActiveProject{ID: p.ID.String(), Slug: p.Slug, Name: p.Name}
		if p.ID == activeID {
			a := summary
			active = &a
			continue
		}
		others = append(others, summary)
	}
	return active, others
}

func uuidFromString(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}

func capitaliseRole(role string) string {
	if role == "" {
		return ""
	}
	return strings.ToUpper(role[:1]) + strings.ToLower(role[1:])
}

// SecretBytes returns cfg.HTTP.StateSecret as bytes.
func (d Deps) SecretBytes() []byte { return []byte(d.Cfg.HTTP.StateSecret) }

// LogErr prints via the injected logger (or the stdlib default when nil).
func (d Deps) LogErr(msg string, err error) {
	if err == nil {
		return
	}
	if d.Logger != nil {
		d.Logger.Printf("%s: %v", msg, err)
		return
	}
	log.Printf("%s: %v", msg, err)
}

// Render writes a templ.Component with a 200 status.
func Render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		log.Printf("render: %v", err)
	}
}

// RenderStatus writes a templ.Component with an explicit status.
func RenderStatus(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := c.Render(r.Context(), w); err != nil {
		log.Printf("render: %v", err)
	}
}

// ErrorPage renders the error.templ full page with the given status/title/message.
func (d Deps) ErrorPage(w http.ResponseWriter, r *http.Request, status int, title, msg string, cause ...error) {
	base := d.Base(r, title)
	RenderStatus(w, r, status, templates.ErrorLayout(base, templates.ErrorView{
		Status: status, Title: title, Message: msg,
		RequestID: reqid.FromContext(r.Context()),
		Code:      ErrorRef(cause...),
	}))
}

// ErrorRef returns the code carried by the first of errs that is an AppError, or "" when none is.
func ErrorRef(errs ...error) string {
	for _, err := range errs {
		if ae, ok := apperror.AsAppError(err); ok {
			return ae.Code()
		}
	}
	return ""
}

// OrgScopeFor resolves the org named by slug and returns a context scoped to it, refusing callers who are not members.
// SECURITY: the slug is attacker-supplied and the handler — not RLS — picks the org, so membership is verified here.
// A non-member gets the same 404 as a bad slug so org slugs cannot be enumerated.
func (d Deps) OrgScopeFor(w http.ResponseWriter, r *http.Request, p session.Principal, slug string) (*org.Org, *http.Request, bool) {
	o, err := d.Orgs.BySlug(r.Context(), slug)
	if err != nil {
		d.ErrorPage(w, r, http.StatusNotFound, "Organization not found", "", err)
		return nil, nil, false
	}
	ctx := tenant.Into(r.Context(), tenant.Context{OrgID: o.ID, UserID: p.UserID})
	if _, err := d.Orgs.MembershipOf(ctx, o.ID, p.UserID); err != nil {
		d.ErrorPage(w, r, http.StatusNotFound, "Organization not found", "", err)
		return nil, nil, false
	}
	return o, r.WithContext(ctx), true
}

// LoadSession reads the sid cookie, verifies its HMAC, and loads the Principal from the store.
func (d Deps) LoadSession(r *http.Request) (session.Principal, string, bool) {
	c, err := r.Cookie(web.SessionCookieName)
	if err != nil {
		return session.Principal{}, "", false
	}
	sid, err := web.VerifyCookie(d.SecretBytes(), c.Value)
	if err != nil {
		return session.Principal{}, "", false
	}
	p, ok, err := d.Sessions.Load(r.Context(), sid)
	if err != nil || !ok {
		return session.Principal{}, "", false
	}
	return p, sid, true
}

// WriteSession saves the Principal under a fresh sid and writes the signed cookie.
func (d Deps) WriteSession(w http.ResponseWriter, r *http.Request, p session.Principal) error {
	sid, err := web.NewSID()
	if err != nil {
		return err
	}
	if err := d.Sessions.Save(r.Context(), sid, p, time.Now().Add(web.SessionTTL)); err != nil {
		return err
	}
	web.SetCookie(w, web.CookieOpts{
		Name:         web.SessionCookieName,
		Value:        web.SignCookie(d.SecretBytes(), sid),
		BasePath:     d.Cfg.HTTP.BasePath,
		CookieSecure: d.Cfg.HTTP.CookieSecure,
		MaxAge:       int(web.SessionTTL.Seconds()),
	})
	return nil
}

// UpdateSession re-saves the Principal under the existing sid, preserving the cookie.
func (d Deps) UpdateSession(r *http.Request, sid string, p session.Principal) error {
	return d.Sessions.Save(r.Context(), sid, p, time.Now().Add(web.SessionTTL))
}

// ClearSession deletes the store row + expires the cookie.
func (d Deps) ClearSession(w http.ResponseWriter, r *http.Request) {
	if _, sid, ok := d.LoadSession(r); ok {
		_ = d.Sessions.Delete(r.Context(), sid)
	}
	web.ClearCookie(w, web.SessionCookieName, d.Cfg.HTTP.BasePath, d.Cfg.HTTP.CookieSecure)
}

// SanitizeReturnTo rejects any return_to that leaves the same-origin.
func SanitizeReturnTo(raw string) string {
	normalized := strings.ReplaceAll(raw, "\\", "/")
	u, err := url.Parse(normalized)
	if err != nil {
		return ""
	}
	if u.Scheme != "" || u.Host != "" || u.Opaque != "" {
		return ""
	}
	if !strings.HasPrefix(u.Path, "/") {
		return ""
	}
	if strings.HasPrefix(u.Path, "//") {
		return ""
	}
	out := u.EscapedPath()
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		out += "#" + u.EscapedFragment()
	}
	return out
}

// ResolveReturnTo returns a safe destination, defaulting to basePath+"/" when raw is unsafe.
func ResolveReturnTo(basePath, raw string) string {
	if s := SanitizeReturnTo(raw); s != "" {
		return web.Path(basePath, s)
	}
	return web.Path(basePath, "/")
}
