package controlplane

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func TestScopeToActiveProject_OutsideGrantIsUnresolved(t *testing.T) {
	orgID, user, granted, other := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	rz := authz.NewResolver(fakes.NewRoles().Set(orgID, user, "member").Restrict(orgID, user, granted))
	ctx := session.PrincipalInto(context.Background(), session.Principal{
		UserID: user, Source: session.SourceToken, ActiveOrgID: orgID, ActiveProjectID: other,
	})

	_, _, err := scopeToActiveProject(ctx, rz, nil, nil, "")
	require.ErrorAs(t, err, new(*ProjectUnresolvedError), "a remembered project outside the grant is treated as unset")
}
