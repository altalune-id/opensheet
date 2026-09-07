package middleware

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/a-h/templ"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/web"
	"altalune.id/template/internal/web/templates"
)

// LayoutFn builds a LayoutData for the shared error page.
type LayoutFn func(r *http.Request, title string) web.LayoutData

// ErrorTemplate renders an *apperror.AppError into an HTTP response.
type ErrorTemplate interface {
	RenderError(w http.ResponseWriter, r *http.Request, err *apperror.AppError) error
}

// TemplateErrorPage is the default ErrorTemplate: full-page HTML for navigation, fragment for HTMX.
type TemplateErrorPage struct {
	Layout LayoutFn
}

// RenderError picks HTML vs HTMX fragment on the HX-Request header.
func (t TemplateErrorPage) RenderError(w http.ResponseWriter, r *http.Request, err *apperror.AppError) error {
	title, msg := "Error", "Unexpected error"
	if err != nil {
		msg = err.Message()
	}
	status := statusFromApp(err)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if isHTMX(r) {
		w.WriteHeader(status)
		_, wErr := w.Write(htmxErrorFragment(status, title, msg))
		return wErr
	}
	if t.Layout != nil {
		data := t.Layout(r, title)
		w.WriteHeader(status)
		return templates.ErrorLayout(data, templates.ErrorView{Status: status, Title: title, Message: msg}).Render(r.Context(), w)
	}
	w.WriteHeader(status)
	//nolint:contextcheck // ErrorPage does not need ctx; write to r.Context() for logging.
	return templates.ErrorPage(web.LayoutData{}, templates.ErrorView{Status: status, Title: title, Message: msg}).Render(r.Context(), w)
}

// LogError writes a plain-text response and, when Log is non-nil, logs the error.
type LogError struct{ Log *slog.Logger }

// RenderError writes an internal-server-error plain-text response.
func (l LogError) RenderError(w http.ResponseWriter, r *http.Request, err *apperror.AppError) error {
	if l.Log != nil {
		code := ""
		msg := ""
		if err != nil {
			code = err.Code()
			msg = err.Message()
		}
		l.Log.ErrorContext(r.Context(), "web.error", "code", code, "message", msg)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(statusFromApp(err))
	if err == nil {
		_, wErr := w.Write([]byte("error"))
		return wErr
	}
	_, wErr := w.Write([]byte(err.Message()))
	return wErr
}

func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

func statusFromApp(_ *apperror.AppError) int {
	return http.StatusInternalServerError
}

func htmxErrorFragment(status int, title, msg string) []byte {
	return []byte(
		`<div class="alt-error" data-status="` + strconv.Itoa(status) + `"><strong>` +
			templ.EscapeString(title) + `</strong> ` + templ.EscapeString(msg) + `</div>`,
	)
}
