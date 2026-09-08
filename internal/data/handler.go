// Package data is the data plane: a published sheet's rows served as JSON to a machine caller holding an API key.
package data

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/sheet"
)

// Reader reads the rows of one published sheet.
type Reader interface {
	Rows(ctx context.Context, sh *sheet.Sheet) (sheet.Rows, error)
}

// Purger drops every cached tab of one sheet.
type Purger interface {
	PurgeCache(ctx context.Context, sheetID uuid.UUID) error
}

// Authorizer decides whether a raw credential may act on one sheet.
type Authorizer interface {
	Authorize(ctx context.Context, raw, scope string, orgID, projectID, sheetID uuid.UUID) (session.Principal, error)
}

// Capabilities reports the config-derived feature flags the data plane must honour.
type Capabilities interface {
	PublicSheetsEnabled() bool
}

// StaleHeader marks a response served from an expired snapshot because the upstream was unreachable.
// NOTE: Warning: 110 is not sent — RFC 9111 obsoleted the Warning header and most clients ignore it.
const StaleHeader = "X-Opensheet-Stale"

// NOTE: sheet.Rows.Warnings is dropped — the body is upstream opensheet's bare array, with nowhere to carry it.

const mountSuffix = "/api/v1"

type handler struct {
	resolver   resolver
	reader     Reader
	purger     Purger
	authz      Authorizer
	caps       Capabilities
	defaultTTL time.Duration
	log        *slog.Logger
}

// NewHandler returns the data plane mounted under basePath+"/api/v1".
func NewHandler(
	basePath string,
	orgs Orgs,
	projects Projects,
	sheets Sheets,
	reader Reader,
	purger Purger,
	authz Authorizer,
	caps Capabilities,
	defaultTTL time.Duration,
	log *slog.Logger,
) http.Handler {
	h := &handler{
		resolver:   resolver{orgs: orgs, projects: projects, sheets: sheets},
		reader:     reader,
		purger:     purger,
		authz:      authz,
		caps:       caps,
		defaultTTL: defaultTTL,
		log:        log.With("surface", "data"),
	}
	inner := http.NewServeMux()
	inner.HandleFunc("GET /orgs/{org}/projects/{project}/sheets/{slug}", h.rows)
	inner.HandleFunc("DELETE /orgs/{org}/projects/{project}/sheets/{slug}/cache", h.purge)
	inner.HandleFunc("/", h.unknown)
	return http.StripPrefix(strings.TrimRight(basePath, "/")+mountSuffix, inner)
}

func (h *handler) rows(w http.ResponseWriter, r *http.Request) {
	// SECURITY: the path is resolved before the caller is authorized — the grant names a sheet id that
	// does not exist until the slugs have been resolved.
	r, sc, err := h.resolve(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if aErr := h.allowRead(r, sc); aErr != nil {
		h.fail(w, r, aErr)
		return
	}
	rows, err := h.reader.Rows(r.Context(), sc.sheet)
	if err != nil {
		h.fail(w, r, maskPublicDisabled(err))
		return
	}
	h.writeRows(w, r, sc.sheet, rows)
}

func (h *handler) purge(w http.ResponseWriter, r *http.Request) {
	// SECURITY: resolve then authorize, for the same reason as rows.
	r, sc, err := h.resolve(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if aErr := h.authorize(r, sc, authn.ScopeCachePurge); aErr != nil {
		h.fail(w, r, aErr)
		return
	}
	if pErr := h.purger.PurgeCache(r.Context(), sc.sheet.ID); pErr != nil {
		h.fail(w, r, pErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) unknown(w http.ResponseWriter, r *http.Request) {
	h.fail(w, r, &NotFoundError{})
}

// NOTE: the scoped request replaces the unscoped one, so a later r.Context() cannot be the wrong scope.
func (h *handler) resolve(r *http.Request) (*http.Request, scope, error) {
	ctx, sc, err := h.resolver.resolve(r.Context(), r.PathValue("org"), r.PathValue("project"), r.PathValue("slug"))
	if err != nil {
		return r, scope{}, err
	}
	return r.WithContext(ctx), sc, nil
}

func (h *handler) allowRead(r *http.Request, sc scope) error {
	if sc.sheet.Visibility != sheet.VisibilityPublic {
		return h.authorize(r, sc, authn.ScopeSheetsRead)
	}
	// SECURITY: the capability gates the read, not only the create, so a sheet published while it was on stops serving once it is off.
	if h.caps.PublicSheetsEnabled() {
		return nil
	}
	return &NotFoundError{cause: publicDisabled()}
}

// SECURITY: every refusal becomes the shared NotFoundError, so a key for another project, a key missing
// the scope, a key not granted this sheet and a slug that does not exist are one indistinguishable answer.
func (h *handler) authorize(r *http.Request, sc scope, scopeName string) error {
	if _, err := h.authz.Authorize(r.Context(), authn.CredentialFrom(r), scopeName, sc.orgID, sc.projectID, sc.sheet.ID); err != nil {
		return &NotFoundError{cause: err}
	}
	return nil
}

func (h *handler) writeRows(w http.ResponseWriter, r *http.Request, sh *sheet.Sheet, rows sheet.Rows) {
	body := []byte("[]")
	if rows.Values != nil {
		marshaled, err := json.Marshal(rows.Values)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		body = marshaled
	}

	head := w.Header()
	head.Set("Content-Type", "application/json; charset=utf-8")
	head.Set("Cache-Control", cacheControl(sh, h.defaultTTL))
	if rows.ETag != "" {
		head.Set("ETag", strconv.Quote(rows.ETag))
	}
	if rows.Cached && !rows.FetchedAt.IsZero() {
		head.Set("Age", strconv.Itoa(ageSeconds(rows.FetchedAt)))
	}
	if rows.Stale {
		head.Set(StaleHeader, "true")
	}

	if rows.ETag != "" && matchesETag(r.Header.Get("If-None-Match"), rows.ETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (h *handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	ae, ok := apperror.AsAppError(err)
	if !ok {
		ae = apperror.New(apperror.CodeUnexpectedError, "An unexpected error occurred", codes.Internal)
	}
	status := ae.HTTPStatus()
	level, msg := slog.LevelInfo, "data: request refused"
	if status >= http.StatusInternalServerError {
		level, msg = slog.LevelError, "data: request failed"
	}
	// NOTE: a masked 404 names its reason here and nowhere else.
	h.log.Log(r.Context(), level, msg,
		"method", r.Method, "path", r.URL.Path, "code", ae.Code(), "err", err)
	body, _ := json.Marshal(errorEnvelope{Error: errorBody{Code: ae.Code(), Message: ae.Message()}})
	head := w.Header()
	head.Set("Content-Type", "application/json; charset=utf-8")
	head.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func cacheControl(sh *sheet.Sheet, defaultTTL time.Duration) string {
	directive := "private"
	if sh.Visibility == sheet.VisibilityPublic {
		directive = "public"
	}
	ttl := sh.CacheTTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	secs := int64(0)
	if ttl > 0 {
		secs = int64(ttl.Seconds())
	}
	return directive + ", max-age=" + strconv.FormatInt(secs, 10)
}

func ageSeconds(fetchedAt time.Time) int {
	age := int(time.Since(fetchedAt).Seconds())
	if age < 0 {
		return 0
	}
	return age
}

func matchesETag(header, etag string) bool {
	if header == "" {
		return false
	}
	quoted := strconv.Quote(etag)
	for tag := range strings.SplitSeq(header, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == quoted {
			return true
		}
	}
	return false
}
