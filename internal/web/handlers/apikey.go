package handlers

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/templates"
)

// APIKeyHandler owns the project, org and personal API key console pages.
type APIKeyHandler struct {
	Deps
	Keys      *apikey.Service
	Resources APIKeyResources
}

// NewAPIKeyHandler wires the handler.
func NewAPIKeyHandler(d Deps, projects *project.Service, keys *apikey.Service) *APIKeyHandler {
	d.Projects = projects
	return &APIKeyHandler{Deps: d, Keys: keys}
}

type listWriter func(minted *templates.APIKeyMinted, errKind, code string)

type keyChange func(ctx context.Context, r *http.Request, id uuid.UUID) error

// GetKeys renders the project API key list page.
func (h *APIKeyHandler) GetKeys(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	v, err := h.projectView(sc, nil, "", "")
	if err != nil {
		h.listFailed(w, sc.req, err)
		return
	}
	Render(w, sc.req, templates.APIKeysLayout(
		h.LayoutForProject(sc.req, "API keys · "+sc.project.Name, sc.org.Slug, sc.project, "apikeys"),
		v,
	))
}

// PostKeyCreate mints a project key and renders the refreshed list with the one-time plaintext reveal.
func (h *APIKeyHandler) PostKeyCreate(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	h.mintInto(w, sc.req, h.projectWriter(w, sc), func(in mintInput) (*apikey.APIKey, string, error) {
		resourceIDs, err := h.pinnedResources(sc, in.resourceIDs)
		if err != nil {
			return nil, "", err
		}
		return h.Keys.Mint(sc.req.Context(), in.name, in.scopes, resourceIDs, in.expiresAt)
	})
}

// PostKeyRevoke revokes a project key and returns the refreshed list fragment.
func (h *APIKeyHandler) PostKeyRevoke(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	h.changeInto(w, sc.req, h.projectWriter(w, sc), h.revoke)
}

// GetOrgKeys renders the org API key list page.
func (h *APIKeyHandler) GetOrgKeys(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return
	}
	v, err := h.orgView(sc, nil, "", "")
	if err != nil {
		h.listFailed(w, sc.req, err)
		return
	}
	Render(w, sc.req, templates.APIKeysLayout(h.LayoutForOrg(sc.req, "API keys · "+sc.org.Name, sc.org.Slug, "apikeys"), v))
}

// PostOrgKeyCreate mints an org key and renders the refreshed list with the one-time plaintext reveal.
func (h *APIKeyHandler) PostOrgKeyCreate(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return
	}
	h.mintInto(w, sc.req, h.orgWriter(w, sc), func(in mintInput) (*apikey.APIKey, string, error) {
		return h.Keys.MintOrg(sc.req.Context(), in.name, in.scopes, in.grant, in.expiresAt)
	})
}

// PostOrgKeyProjects widens an org key by the submitted projects.
func (h *APIKeyHandler) PostOrgKeyProjects(w http.ResponseWriter, r *http.Request) {
	h.changeOrgKey(w, r, h.grantProjects)
}

// PostOrgKeyAllProjects promotes an org key to every project of the org.
func (h *APIKeyHandler) PostOrgKeyAllProjects(w http.ResponseWriter, r *http.Request) {
	h.changeOrgKey(w, r, h.grantAllProjects)
}

// PostOrgKeyRevoke revokes an org key and returns the refreshed list fragment.
func (h *APIKeyHandler) PostOrgKeyRevoke(w http.ResponseWriter, r *http.Request) {
	h.changeOrgKey(w, r, h.revoke)
}

func (h *APIKeyHandler) changeOrgKey(w http.ResponseWriter, r *http.Request, change keyChange) {
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return
	}
	h.changeInto(w, sc.req, h.orgWriter(w, sc), change)
}

func (h *APIKeyHandler) grantProjects(ctx context.Context, r *http.Request, id uuid.UUID) error {
	_, err := h.Keys.GrantProjects(ctx, id, formUUIDs(r.PostForm["project_ids"]))
	return err
}

func (h *APIKeyHandler) grantAllProjects(ctx context.Context, _ *http.Request, id uuid.UUID) error {
	_, err := h.Keys.GrantAllProjects(ctx, id)
	return err
}

func (h *APIKeyHandler) revoke(ctx context.Context, _ *http.Request, id uuid.UUID) error {
	if err := h.Keys.Revoke(ctx, id); err != nil && !apikey.IsNotFoundError(err) {
		return err
	}
	return nil
}

type mintInput struct {
	name        string
	scopes      []string
	grant       apikey.ProjectGrant
	resourceIDs []string
	expiresAt   *time.Time
}

// SECURITY: the plaintext is rendered once and discarded — never logged, stored on a row, or put in the session.
func (h *APIKeyHandler) mintInto(w http.ResponseWriter, r *http.Request, write listWriter, mint func(mintInput) (*apikey.APIKey, string, error)) {
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	expiresAt, bad := expiryFor(r.PostForm.Get("expires_in"), time.Now().UTC())
	if bad {
		write(nil, templates.APIKeyErrorInvalidExpiry, "")
		return
	}
	in := mintInput{
		name:        strings.TrimSpace(r.PostForm.Get("name")),
		scopes:      selectedScopes(r.PostForm["scopes"]),
		grant:       formGrant(r),
		resourceIDs: r.PostForm["resource_ids"],
		expiresAt:   expiresAt,
	}
	k, plaintext, err := mint(in)
	if err != nil {
		h.LogErr("web apikey: mint", err)
		write(nil, apiKeyErrorKind(err), ErrorRef(err))
		return
	}
	write(&templates.APIKeyMinted{Name: k.Name, Plaintext: plaintext}, "", "")
}

func (h *APIKeyHandler) changeInto(w http.ResponseWriter, r *http.Request, write listWriter, change keyChange) {
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad id", "Malformed API key id.")
		return
	}
	if err := change(r.Context(), r, id); err != nil {
		h.LogErr("web apikey: change", err)
		write(nil, apiKeyErrorKind(err), ErrorRef(err))
		return
	}
	write(nil, "", "")
}

func (h *APIKeyHandler) canManage(r *http.Request, orgID, userID uuid.UUID) bool {
	ok, err := h.Orgs.IsManager(r.Context(), orgID, userID)
	if err != nil {
		h.LogErr("web apikey: role", err)
	}
	return ok
}

func baseView(base string, minted *templates.APIKeyMinted, errKind, code string) templates.APIKeysView {
	return templates.APIKeysView{
		Base:      base,
		Scopes:    scopeOptions(),
		Expiries:  expiryOptions(),
		Minted:    minted,
		ErrorKind: errKind,
		ErrorCode: code,
	}
}

func (h *APIKeyHandler) projectView(sc ProjectScope, minted *templates.APIKeyMinted, errKind, code string) (templates.APIKeysView, error) {
	items, err := h.Keys.List(sc.req.Context(), sc.project.ID)
	if err != nil {
		return templates.APIKeysView{}, err
	}
	resources, err := h.resources(sc)
	if err != nil {
		return templates.APIKeysView{}, err
	}
	v := baseView(h.ProjectURL(sc, "/apikeys"), minted, errKind, code)
	v.CanManage = h.canManage(sc.req, sc.org.ID, sc.principal.UserID)
	v.Resources = resourceView(resources)
	for _, k := range items {
		row := apiKeyRow(k, v.Base, nil)
		if h.Resources != nil {
			row.HasResources, row.ResourceNames = true, resourceNames(k.ResourceIDs, resources)
		}
		v.Items = append(v.Items, row)
	}
	return v, nil
}

func (h *APIKeyHandler) orgView(sc OrgScope, minted *templates.APIKeyMinted, errKind, code string) (templates.APIKeysView, error) {
	items, err := h.Keys.ListOrg(sc.req.Context())
	if err != nil {
		return templates.APIKeysView{}, err
	}
	options, err := h.projectOptions(sc)
	if err != nil {
		return templates.APIKeysView{}, err
	}
	v := baseView(web.Path(h.Cfg.HTTP.BasePath, "/orgs/"+sc.org.Slug+"/apikeys"), minted, errKind, code)
	v.OrgLevel, v.HasGrant, v.Projects = true, true, options
	v.CanManage = h.canManage(sc.req, sc.org.ID, sc.principal.UserID)
	for _, k := range items {
		v.Items = append(v.Items, apiKeyRow(k, v.Base, options))
	}
	return v, nil
}

func (h *APIKeyHandler) projectWriter(w http.ResponseWriter, sc ProjectScope) listWriter {
	return func(minted *templates.APIKeyMinted, errKind, code string) {
		v, err := h.projectView(sc, minted, errKind, code)
		if err != nil {
			h.listFailed(w, sc.req, err)
			return
		}
		Render(w, sc.req, templates.APIKeyList(h.ProjectFragmentBase(sc), v))
	}
}

func (h *APIKeyHandler) orgWriter(w http.ResponseWriter, sc OrgScope) listWriter {
	return func(minted *templates.APIKeyMinted, errKind, code string) {
		v, err := h.orgView(sc, minted, errKind, code)
		if err != nil {
			h.listFailed(w, sc.req, err)
			return
		}
		Render(w, sc.req, templates.APIKeyList(h.OrgFragmentBase(sc), v))
	}
}

func (h *APIKeyHandler) listFailed(w http.ResponseWriter, r *http.Request, err error) {
	h.LogErr("web apikey: list", err)
	h.ErrorPage(w, r, http.StatusInternalServerError, "List failed", "Could not load API keys.", err)
}

// Register wires the API key routes onto mux.
func (h *APIKeyHandler) Register(mux web.Mux) {
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/apikeys", h.GetKeys)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/apikeys", h.PostKeyCreate)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/apikeys/{id}/revoke", h.PostKeyRevoke)
	mux.HandleFunc("GET /orgs/{org}/apikeys", h.GetOrgKeys)
	mux.HandleFunc("POST /orgs/{org}/apikeys", h.PostOrgKeyCreate)
	mux.HandleFunc("POST /orgs/{org}/apikeys/{id}/projects", h.PostOrgKeyProjects)
	mux.HandleFunc("POST /orgs/{org}/apikeys/{id}/all-projects", h.PostOrgKeyAllProjects)
	mux.HandleFunc("POST /orgs/{org}/apikeys/{id}/revoke", h.PostOrgKeyRevoke)
	mux.HandleFunc("GET /settings/tokens", h.GetPersonalTokens)
	mux.HandleFunc("GET /settings/tokens/projects", h.GetPersonalTokenProjects)
	mux.HandleFunc("POST /settings/tokens", h.PostPersonalTokenCreate)
	mux.HandleFunc("POST /settings/tokens/{org}/{id}/projects", h.PostPersonalTokenProjects)
	mux.HandleFunc("POST /settings/tokens/{org}/{id}/all-projects", h.PostPersonalTokenAllProjects)
	mux.HandleFunc("POST /settings/tokens/{org}/{id}/revoke", h.PostPersonalTokenRevoke)
}

func formGrant(r *http.Request) apikey.ProjectGrant {
	if r.PostForm.Get("grant") == "all" {
		return apikey.ProjectGrant{All: true}
	}
	return apikey.ProjectGrant{ProjectIDs: formUUIDs(r.PostForm["project_ids"])}
}

func formUUIDs(raw []string) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		if id, err := uuid.Parse(strings.TrimSpace(s)); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// SECURITY: an unrecognized or duplicated scope value is dropped rather than reaching Mint.
func selectedScopes(raw []string) []string {
	set := make(map[string]bool, len(raw))
	for _, s := range raw {
		set[strings.TrimSpace(s)] = true
	}
	out := make([]string, 0, len(raw))
	for _, s := range authn.MintableScopes() {
		if set[s] {
			out = append(out, s)
		}
	}
	return out
}

var expiryDays = []string{"7", "30", "60", "90", "180", "365"} //nolint:gochecknoglobals // immutable option list.

const defaultExpiryDays = "30"

func expiryFor(raw string, now time.Time) (*time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if !slices.Contains(expiryDays, raw) {
		return nil, true
	}
	days, err := strconv.Atoi(raw)
	if err != nil {
		return nil, true
	}
	t := now.AddDate(0, 0, days)
	return &t, false
}

//i18n:use apikey.expiry.*
func expiryOptions() []templates.APIKeyExpiryOption {
	out := make([]templates.APIKeyExpiryOption, 0, len(expiryDays))
	for _, d := range expiryDays {
		out = append(out, templates.APIKeyExpiryOption{Days: d, LabelKey: "apikey.expiry." + d, Selected: d == defaultExpiryDays})
	}
	return out
}

func apiKeyErrorKind(err error) string {
	switch {
	case org.IsNotManagerError(err):
		return templates.APIKeyErrorNotManager
	case apikey.IsUnknownScopeError(err), apikey.IsRetiredScopeError(err), apikey.IsScopeLevelError(err):
		return templates.APIKeyErrorScope
	case apikey.IsNotFoundError(err), org.IsMembershipMissingError(err):
		return templates.APIKeyErrorNotFound
	case apikey.IsExpiryRequiredError(err), apikey.IsExpiryInPastError(err), apikey.IsExpiryTooLongError(err):
		return templates.APIKeyErrorInvalidExpiry
	case IsResourceNotInProjectError(err):
		return templates.APIKeyErrorResource
	case apikey.IsEmptyGrantError(err):
		return templates.APIKeyErrorEmptyGrant
	case apikey.IsProjectNotInOrgError(err), apikey.IsAlreadyAllProjectsError(err), apikey.IsBoundToProjectError(err),
		apikey.IsGrantConflictError(err), apikey.IsRevokedError(err):
		return templates.APIKeyErrorGrant
	default:
		return templates.APIKeyErrorFailed
	}
}

func (h *APIKeyHandler) projectOptions(sc OrgScope) ([]templates.APIKeyProjectOption, error) {
	projects, err := h.Projects.List(sc.req.Context(), sc.org.ID)
	if err != nil {
		return nil, err
	}
	options := make([]templates.APIKeyProjectOption, 0, len(projects))
	for _, p := range projects {
		options = append(options, templates.APIKeyProjectOption{ID: p.ID.String(), Name: p.Name})
	}
	return options, nil
}

func apiKeyRow(k *apikey.APIKey, base string, projects []templates.APIKeyProjectOption) templates.APIKeyRow {
	labels := make([]string, 0, len(k.Scopes))
	for _, s := range k.Scopes {
		labels = append(labels, scopeLabelKey(s))
	}
	row := templates.APIKeyRow{
		ID:             k.ID.String(),
		Base:           base,
		Name:           k.Name,
		SecretHint:     k.SecretHint,
		ScopeLabelKeys: labels,
		HasGrant:       k.HasGrant(),
		AllProjects:    k.AllProjects,
		CreatedAt:      k.CreatedAt.Format(time.RFC3339),
		Revoked:        k.RevokedAt != nil,
	}
	granted := make([]string, 0, len(k.ProjectIDs))
	for _, id := range k.ProjectIDs {
		granted = append(granted, id.String())
	}
	for _, p := range projects {
		if slices.Contains(granted, p.ID) {
			row.ProjectNames = append(row.ProjectNames, p.Name)
			continue
		}
		row.Grantable = append(row.Grantable, p)
	}
	if k.ExpiresAt != nil {
		row.HasExpiry = true
		row.ExpiresAt = k.ExpiresAt.Format(time.RFC3339)
	}
	if k.LastUsedAt != nil {
		row.HasLastUsed = true
		row.LastUsedAt = k.LastUsedAt.Format(time.RFC3339)
	}
	if k.RevokedAt != nil {
		row.RevokedAt = k.RevokedAt.Format(time.RFC3339)
	}
	return row
}

func scopeOptions() []templates.APIKeyScopeOption {
	all := authn.MintableScopes()
	out := make([]templates.APIKeyScopeOption, 0, len(all))
	for _, s := range all {
		out = append(out, templates.APIKeyScopeOption{Value: s, LabelKey: scopeLabelKey(s)})
	}
	return out
}

//i18n:use apikey.scope.*
func scopeLabelKey(scope string) string {
	return "apikey.scope." + strings.ReplaceAll(scope, ":", "_")
}
