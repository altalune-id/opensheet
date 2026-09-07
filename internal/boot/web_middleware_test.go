package boot

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The tenant middleware is what makes r.Context() carry a tenant scope. Without it every handler
// must remember tenant.Into, and the ones that forget fail at runtime with tenant: missing context.
func TestWebHandler_InstallsTenantMiddlewareAfterSession(t *testing.T) {
	src, err := os.ReadFile("http.go")
	require.NoError(t, err)

	chain := string(src)
	start := strings.Index(chain, "Middlewares: []web.Middleware{")
	require.Positive(t, start, "middleware chain not found — this guard needs updating")
	end := strings.Index(chain[start:], "\n\t\t},")
	require.Positive(t, end, "middleware chain end not found — this guard needs updating")
	chain = chain[start : start+end]

	session := strings.Index(chain, "webmw.Session(")
	tenant := strings.Index(chain, "webmw.Tenant")
	require.Positive(t, session, "session middleware missing from the chain")
	require.Positive(t, tenant, "tenant middleware missing from the chain — handlers would see an unscoped r.Context()")
	require.Less(t, session, tenant, "tenant must run after session or the principal is not on the context yet")
}
