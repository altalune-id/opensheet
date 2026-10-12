package boot_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
)

func TestMCP_UndecodableArgumentsAnswerAValidationErrorNotAnIncident(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	names := f.srv.MCP.Registry().Names()
	require.NotEmpty(t, names, "boot registered no MCP tools")
	tool := names[0]
	spec, ok := f.srv.MCP.Registry().Spec(tool)
	require.True(t, ok)
	token := f.issuer.mint(t, mcpAudience, []string{spec.Scope})

	rec := f.call(t, token, callToolBody(tool, map[string]any{"bogus": true}))

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	payload := toolPayload(t, rec)
	require.Equal(t, apperror.CodeValidation, payload.Code, "payload=%+v", payload)
	require.Equal(t, map[string]string{"tool": tool, "field": "bogus"}, payload.Meta)
	require.False(t, f.log.warned("mcp: unmapped tool failure"),
		"a client's malformed arguments were logged as an unmapped incident")
}
