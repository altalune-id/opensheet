package handlers

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/web/templates"
)

// maxKeyUpload bounds a service-account key upload; a real key is a few kilobytes.
const maxKeyUpload = 64 << 10

// CredentialHandler owns the project-scoped credential pages.
type CredentialHandler struct {
	Deps
	Credentials *credential.Service
	Connect     *credential.ConnectWorkflow
}

// NewCredentialHandler wires the handler.
func NewCredentialHandler(d Deps, projects *project.Service, credentials *credential.Service, connect *credential.ConnectWorkflow) *CredentialHandler {
	d.Projects = projects
	return &CredentialHandler{Deps: d, Credentials: credentials, Connect: connect}
}

// GetList renders the credentials page.
func (h *CredentialHandler) GetList(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireProject(w, r)
	if !ok {
		return
	}
	h.remember(sc)
	view, ok := h.view(w, sc)
	if !ok {
		return
	}
	Render(w, sc.req, templates.CredentialsLayout(
		h.LayoutForProject(sc.req, "Credentials · "+sc.project.Name, sc.org.Slug, sc.project, "credentials"),
		view,
	))
}

// PostUpload stores an uploaded service-account key and returns the refreshed section.
func (h *CredentialHandler) PostUpload(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireProject(w, r)
	if !ok {
		return
	}
	name, raw, msg := keyUpload(sc.req)
	if msg != "" {
		h.writeSection(w, sc, templates.CredentialsView{Name: name, Error: msg})
		return
	}
	if _, err := h.Credentials.UploadServiceAccount(sc.req.Context(), name, raw); err != nil {
		h.LogErr("web credential: upload", err)
		h.writeSection(w, sc, templates.CredentialsView{Name: name, Error: uploadMessage(err), ErrorCode: ErrorRef(err)})
		return
	}
	h.writeSection(w, sc, templates.CredentialsView{StoredUnverified: true})
}

// PostDelete removes a credential and returns the refreshed section.
func (h *CredentialHandler) PostDelete(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireProject(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, sc.req, "credential")
	if !ok {
		return
	}
	if err := h.Credentials.Delete(sc.req.Context(), id); err != nil && !credential.IsNotFoundError(err) {
		h.LogErr("web credential: delete", err)
		h.writeSection(w, sc, templates.CredentialsView{Error: deleteMessage(err), ErrorCode: ErrorRef(err)})
		return
	}
	h.writeSection(w, sc, templates.CredentialsView{})
}

// PostGoogleStart redirects the browser into Google's consent screen.
func (h *CredentialHandler) PostGoogleStart(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireProject(w, r)
	if !ok {
		return
	}
	// SECURITY: the capability is enforced here, not only by hiding the button.
	if !h.Caps.GoogleConnect {
		h.ErrorPage(w, sc.req, http.StatusNotFound, "Not available", "This deployment has no Google OAuth client configured.")
		return
	}
	returnTo := projectPath(sc.org.Slug, sc.project.Slug, "/credentials")
	authURL, err := h.Connect.Start(sc.req.Context(), sc.org.ID, sc.project.ID, sc.principal.UserID, returnTo)
	if err != nil {
		h.LogErr("web credential: connect start", err)
		h.ErrorPage(w, sc.req, http.StatusBadGateway, "Connect failed", "Could not start the Google connect flow.", err)
		return
	}
	http.Redirect(w, sc.req, authURL, http.StatusSeeOther)
}

// Register wires the credential routes onto mux.
func (h *CredentialHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/credentials", h.GetList)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/credentials", h.PostUpload)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/credentials/google/start", h.PostGoogleStart)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/credentials/{id}/delete", h.PostDelete)
}

func (h *CredentialHandler) writeSection(w http.ResponseWriter, sc projectScope, view templates.CredentialsView) {
	filled, ok := h.fill(w, sc, view)
	if !ok {
		return
	}
	Render(w, sc.req, templates.CredentialSection(h.fragment(sc), filled))
}

func (h *CredentialHandler) view(w http.ResponseWriter, sc projectScope) (templates.CredentialsView, bool) {
	return h.fill(w, sc, templates.CredentialsView{})
}

func (h *CredentialHandler) fill(w http.ResponseWriter, sc projectScope, view templates.CredentialsView) (templates.CredentialsView, bool) {
	items, err := h.Credentials.List(sc.req.Context())
	if err != nil {
		h.LogErr("web credential: list", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "List failed", "Could not load credentials.", err)
		return templates.CredentialsView{}, false
	}
	view.ProjectSlug = sc.project.Slug
	view.Rows = make([]templates.CredentialRow, 0, len(items))
	for _, c := range items {
		view.Rows = append(view.Rows, templates.CredentialRow{
			ID:           c.ID.String(),
			Name:         c.Name,
			KindLabel:    kindLabel(c.Kind),
			Account:      c.GoogleAccountEmail,
			CreatedAt:    c.CreatedAt.Format("2006-01-02"),
			NeedsReauth:  c.Status == credential.StatusReauthNeeded,
			CanReconnect: c.Kind == credential.KindGoogleOAuth,
		})
	}
	return view, true
}

func kindLabel(k credential.Kind) string {
	if k == credential.KindGoogleOAuth {
		return "Google account"
	}
	return "Service account"
}

// keyUpload reads the key from the file part, falling back to the pasted textarea.
// SECURITY: the payload is a private key, so it is never logged and never echoed into a message.
func keyUpload(r *http.Request) (name string, key []byte, failure string) {
	if err := r.ParseMultipartForm(maxKeyUpload); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		return "", nil, "Could not read the upload."
	}
	name = strings.TrimSpace(r.FormValue("name"))
	if pasted := strings.TrimSpace(r.FormValue("key_json")); pasted != "" {
		return name, []byte(pasted), ""
	}
	file, _, err := r.FormFile("key")
	if err != nil {
		return name, nil, "Attach a service-account key file, or paste its JSON."
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maxKeyUpload))
	if err != nil || len(raw) == 0 {
		return name, nil, "Attach a service-account key file, or paste its JSON."
	}
	return name, raw, ""
}

func uploadMessage(err error) string {
	switch {
	case credential.IsInvalidNameError(err),
		credential.IsInvalidKindError(err),
		credential.IsInvalidServiceAccountError(err),
		credential.IsAlreadyExistsError(err):
		return err.Error()
	case sealer.IsUnavailableError(err):
		return "Encryption is not configured, so credentials cannot be stored."
	}
	return "Could not store that credential."
}

func deleteMessage(err error) string {
	if credential.IsInUseError(err) {
		return "A registered spreadsheet still uses this credential."
	}
	return "Could not delete that credential."
}
