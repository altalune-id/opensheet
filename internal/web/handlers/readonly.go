package handlers

import (
	"errors"
	"net/http"
	"slices"

	"altalune.id/opensheet/internal/i18n"
	"altalune.id/opensheet/internal/platform/authz"
)

//i18n:use errors.not_allowed.title
//i18n:use members.read_only
//i18n:use invites.read_only
//i18n:use org.rename_managers_only
//i18n:use projects.create_managers_only
//i18n:use projects.rename_managers_only
//i18n:use writes.managers_only
//i18n:use credentials.delete_managers_only

func holds(r *http.Request, p authz.Permission) bool {
	return slices.Contains(authz.Held(r.Context()), p)
}

func (d Deps) refuseRole(w http.ResponseWriter, r *http.Request, reasonKey string, cause ...error) {
	tr := i18n.TranslatorFrom(r.Context())
	d.ErrorPage(w, r, http.StatusForbidden, tr.T("errors.not_allowed.title"), tr.T(reasonKey), cause...)
}

func deniedWrites(err error) bool {
	d, ok := errors.AsType[*authz.DeniedError](err)
	return ok && (d.Permission == authz.SheetsEnableWrites || d.Permission == authz.SpreadsheetsEnableWrites)
}
