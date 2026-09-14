package handlers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/templates"
)

// googlePickerPath is one of the two Google routes that cannot sit under a project: Google requires
// an exact-match redirect URI, so the tenant travels in a signed state instead of the path.
const googlePickerPath = "/credentials/google/picker"

// driveFileScope is the only Drive scope opensheet requests.
// https://developers.google.com/workspace/sheets/api/scopes
const driveFileScope = "https://www.googleapis.com/auth/drive.file"

var errPickerState = errors.New("web: picker state invalid")

// GoogleConnectHandler owns the fixed Google callback and the Picker page.
type GoogleConnectHandler struct {
	Deps
	Credentials *credential.Service
	Connect     *credential.ConnectWorkflow
}

// NewGoogleConnectHandler wires the handler.
func NewGoogleConnectHandler(d Deps, projects *project.Service, credentials *credential.Service, connect *credential.ConnectWorkflow) *GoogleConnectHandler {
	d.Projects = projects
	return &GoogleConnectHandler{Deps: d, Credentials: credentials, Connect: connect}
}

// pickerState is the tenancy the Picker page acts on, signed because the path cannot carry it.
type pickerState struct {
	Org          string `json:"org"`
	Project      string `json:"project"`
	CredentialID string `json:"credential_id"`
}

// PickerStateFor signs the org, project and credential the Picker page will act on.
// SECURITY: the state names a tenant, it does not grant one — the page re-checks membership through OrgScopeFor.
func PickerStateFor(secret []byte, orgSlug, projectSlug string, credentialID uuid.UUID) string {
	raw, err := json.Marshal(pickerState{Org: orgSlug, Project: projectSlug, CredentialID: credentialID.String()})
	if err != nil {
		return ""
	}
	return web.SignCookie(secret, base64.RawURLEncoding.EncodeToString(raw))
}

func decodePickerState(secret []byte, signed string) (pickerState, error) {
	value, err := web.VerifyCookie(secret, signed)
	if err != nil {
		return pickerState{}, errPickerState
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return pickerState{}, errPickerState
	}
	var st pickerState
	if err := json.Unmarshal(raw, &st); err != nil {
		return pickerState{}, errPickerState
	}
	if st.Org == "" || st.Project == "" {
		return pickerState{}, errPickerState
	}
	return st, nil
}

// GetCallback completes a Google consent and returns the browser to where the connect started.
func (h *GoogleConnectHandler) GetCallback(w http.ResponseWriter, r *http.Request) {
	if !h.Caps.GoogleConnect {
		h.ErrorPage(w, r, http.StatusNotFound, "Not available", "This deployment has no Google OAuth client configured.")
		return
	}
	q := r.URL.Query()
	state := q.Get("state")
	returnTo, err := h.Connect.ReturnTo(state)
	if err != nil {
		h.LogErr("web google connect: state", err)
		h.ErrorPage(w, r, http.StatusBadRequest, "Connect link expired",
			"Start the Google connection again from the credentials page.", err)
		return
	}
	if err := checkDriveFileGranted(q.Get("scope")); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Google Drive access is required",
			"opensheet needs the Google Drive file permission to read the sheets you pick. Connect again and leave that permission ticked.")
		return
	}
	if _, err := h.Connect.Complete(r.Context(), q.Get("code"), state); err != nil {
		h.LogErr("web google connect: complete", err)
		h.ErrorPage(w, r, connectStatus(err), "Connect failed", connectMessage(err), err)
		return
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, returnTo), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
}

// GetPicker renders the Google Picker for a connected account.
func (h *GoogleConnectHandler) GetPicker(w http.ResponseWriter, r *http.Request) {
	if !h.Caps.GooglePicker {
		h.ErrorPage(w, r, http.StatusNotFound, "Not available", "This deployment has no Google Picker API key configured.")
		return
	}
	st, err := decodePickerState(h.SecretBytes(), r.URL.Query().Get("state"))
	if err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad picker link", "Open the Picker from the spreadsheets page.")
		return
	}
	sc, ok := h.requireProjectSlugs(w, r, st.Org, st.Project)
	if !ok {
		return
	}
	credID, err := uuid.Parse(st.CredentialID)
	if err != nil {
		h.ErrorPage(w, sc.req, http.StatusBadRequest, "Bad picker link", "Open the Picker from the spreadsheets page.")
		return
	}
	token, ok := h.browserToken(w, sc, credID)
	if !ok {
		return
	}
	Render(w, sc.req, templates.GooglePickerLayout(
		h.LayoutForProject(sc.req, "Pick a spreadsheet · "+sc.project.Name, sc.org.Slug, sc.project, "spreadsheets"),
		templates.GooglePickerView{
			ProjectSlug: sc.project.Slug,
			APIKey:      h.Cfg.Google.Picker.APIKey,
			AppID:       gworkspace.ProjectNumber(h.Cfg.Google.OAuth.ClientID),
			AccessToken: token,
			ReturnURL:   web.Path(h.Cfg.HTTP.BasePath, projectPath(sc.org.Slug, sc.project.Slug, "/spreadsheets")),
		},
	))
}

// Register wires the two fixed Google routes onto mux.
func (h *GoogleConnectHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /credentials/google/callback", h.GetCallback)
	mux.HandleFunc("GET /credentials/google/picker", h.GetPicker)
}

// browserToken mints the access token the Picker runs on.
// SECURITY: only a google_oauth grant is eligible — a service-account token would let the browser act as the service account.
func (h *GoogleConnectHandler) browserToken(w http.ResponseWriter, sc projectScope, credID uuid.UUID) (string, bool) {
	c, err := h.Credentials.ByID(sc.req.Context(), credID)
	if err != nil {
		h.ErrorPage(w, sc.req, http.StatusNotFound, "Credential not found", "That credential no longer exists in this project.", err)
		return "", false
	}
	if c.Kind != credential.KindGoogleOAuth {
		h.ErrorPage(w, sc.req, http.StatusConflict, "Not a Google account",
			"The Picker needs a connected Google account. A service-account credential cannot open it.")
		return "", false
	}
	ts, err := h.Credentials.TokenSourceFor(sc.req.Context(), credID, driveFileScope)
	if err != nil {
		h.LogErr("web google picker: token source", err)
		h.ErrorPage(w, sc.req, http.StatusBadGateway, "Google is unreachable", connectMessage(err), err)
		return "", false
	}
	token, err := ts.Token()
	if err != nil {
		h.LogErr("web google picker: token", err)
		h.ErrorPage(w, sc.req, http.StatusBadGateway, "Google is unreachable",
			"Could not mint an access token for that Google account. Reconnect it and try again.")
		return "", false
	}
	return token.AccessToken, true
}

// checkDriveFileGranted refuses a consent the user narrowed below what a read needs.
// NOTE: Google echoes the granted scopes on the callback; an absent parameter is not treated as a
// refusal, because that would turn a change in Google's response shape into a total connect outage.
func checkDriveFileGranted(raw string) error {
	granted := strings.Fields(raw)
	if len(granted) == 0 || slices.Contains(granted, driveFileScope) {
		return nil
	}
	return errors.New("web: google grant is missing the drive.file scope")
}

func connectStatus(err error) int {
	switch {
	case credential.IsStateInvalidError(err), credential.IsNoRefreshTokenError(err):
		return http.StatusBadRequest
	case credential.IsNotConfiguredError(err):
		return http.StatusNotFound
	}
	return http.StatusBadGateway
}

func connectMessage(err error) string {
	switch {
	case credential.IsStateInvalidError(err):
		return "Start the Google connection again from the credentials page."
	case credential.IsNoRefreshTokenError(err):
		return "Google returned no refresh token. Remove opensheet's access in your Google account, then connect again."
	case credential.IsNotConfiguredError(err):
		return "This deployment has no Google OAuth client configured."
	case credential.IsReauthNeededError(err):
		return "That credential needs reconnecting."
	}
	return "Google rejected the connection. Try again."
}
