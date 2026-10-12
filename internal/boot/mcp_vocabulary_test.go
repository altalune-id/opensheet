package boot_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	mcpinternal "altalune.id/opensheet/internal/mcp"
	rootmcp "altalune.id/opensheet/mcp"
	"altalune.id/opensheet/reqid"
)

// TestMCP_OneErrorVocabularyAcrossTheSurface pins the 401 body and an in-result tool failure to one struct, one code registry and one request id, so a host needs a single parser.
func TestMCP_OneErrorVocabularyAcrossTheSurface(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})

	t.Run("the 401 body", func(t *testing.T) {
		const id = "mcp-401-vocabulary-0123456789"
		rec := f.callWithRequestID(t, "", id, listToolsBody())

		require.Equal(t, http.StatusUnauthorized, rec.Code, "body=%s", rec.Body.String())
		require.Equal(t, "no-store", rec.Header().Get("Cache-Control"),
			"an intermediary may cache the 401 and answer it to the next caller")

		var payload rootmcp.ErrorPayload
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload),
			"the 401 body is not an mcp.ErrorPayload; body=%s", rec.Body.String())
		require.Equal(t, apperror.CodeMCPUnauthenticated, payload.Code)
		require.Equal(t, id, payload.RequestID,
			"the 401 carries no request id, so a report cannot be matched to a log line")
	})

	t.Run("an in-result tool failure", func(t *testing.T) {
		const id = "mcp-result-vocabulary-0123456789"
		rec := f.callWithRequestID(t, f.noneKey, id,
			callToolBody(mcpinternal.ToolProjectList, map[string]any{}))

		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		payload := toolPayload(t, rec)
		require.Equal(t, apperror.CodeForbidden, payload.Code)
		require.Equal(t, id, payload.RequestID,
			"the in-result payload carries no request id, so it cannot be matched to a log line")
	})
}

func (f *mcpFixture) callWithRequestID(t *testing.T, credential, requestID string, body any) *httptest.ResponseRecorder {
	t.Helper()

	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, mcpPath, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", mcpProtocolHeader)
	req.Header.Set(reqid.Header, requestID)
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	rec := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(rec, req)
	return rec
}

// TestMCP_BootAnnouncesTheMountedSurface keeps the surface visible in the logs: an operator reading boot output must see that /mcp mounted, at what audience and with the UI on or off.
func TestMCP_BootAnnouncesTheMountedSurface(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true, appsUI: true})

	require.True(t, f.log.warned("boot: mcp surface mounted"),
		"boot logged no line for the MCP surface; its only line is a warning on the failure path")

	off := newMCPFixture(t, mcpOpts{enabled: false})
	require.False(t, off.log.warned("boot: mcp surface mounted"),
		"boot announced an MCP surface it never mounted")
}
