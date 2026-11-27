package boot_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/boot"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
)

func bindAs(t *testing.T, srv *boot.Server, tc tenant.Context) context.Context {
	t.Helper()
	ctx, err := srv.Authz.Bind(t.Context(), session.Principal{UserID: tc.UserID, Source: session.SourceOIDC}, tc)
	require.NoError(t, err)
	return ctx
}
