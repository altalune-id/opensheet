package handlers_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOverview_ShowsCopyableIdentifiers(t *testing.T) {
	x := newTodoFixture(t)

	rec := x.do(t, http.MethodGet, todoBase+"/overview", "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, x.project.String(), "overview must show the project UUID")
	assert.Contains(t, body, `data-copy="`+x.project.String()+`"`, "project UUID needs a copy button")
	assert.Contains(t, body, `data-copy="alpha"`, "project slug needs a copy button")
	assert.Contains(t, body, `data-copy="acme"`, "org slug needs a copy button")
	assert.Contains(t, body, "font-mono", "the identifiers are rendered in a monospace face")
	assert.Len(t, namedCopyControl.FindAllString(body, -1), 3,
		"org slug, project slug and project id must each be a copy control with an accessible name")
}

var namedCopyControl = regexp.MustCompile(`<button[^>]*data-copy="[^"]*"[^>]*aria-label="[^"]+"`)

func TestOverview_LinksToPosts(t *testing.T) {
	x := newTodoFixture(t)

	rec := x.do(t, http.MethodGet, todoBase+"/overview", "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, `href="`+todoBase+`/posts"`, "quick actions must link to posts")
	assert.Contains(t, body, "overview.open_posts", "the posts action needs its own translated label")
	assert.Equal(t, 1, strings.Count(body, "bg-primary px-4 py-2"), "at most one primary quick action")
}

var noncedCopyScript = regexp.MustCompile(`<script nonce="[^"]*">[^<]*navigator\.clipboard`)

func TestOverview_CopyScriptCarriesNonce(t *testing.T) {
	x := newTodoFixture(t)
	x.Cfg.HTTP.CSP.Enabled = true

	rec := x.do(t, http.MethodGet, todoBase+"/overview", "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	require.Contains(t, body, "data-copied-label", "the copy buttons must be on the page")
	assert.Regexp(t, noncedCopyScript, body, "the copy script must carry a nonce or CSP silently drops it")
}
