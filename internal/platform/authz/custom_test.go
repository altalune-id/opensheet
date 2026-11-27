package authz_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func TestResolver_CustomRoleHoldsExactlyItsPermissions(t *testing.T) {
	orgID, user, roleID := uuid.New(), uuid.New(), uuid.New()
	perms := []authz.Permission{authz.SheetsRead, authz.MembersWrite, authz.RolesWrite}
	src := fakes.NewRoles().SetCustom(orgID, user, roleID, perms...)

	ctx := bindIn(t, src, orgID, session.Principal{UserID: user, Source: session.SourceOIDC})
	require.ElementsMatch(t, perms, authz.Held(ctx))
	require.Equal(t, "custom", session.PrincipalFrom(ctx).HeldRole)

	got, err := authz.NewResolver(src).PermissionsOf(t.Context(), "custom", roleID)
	require.NoError(t, err)
	require.ElementsMatch(t, perms, got)
	got, err = authz.NewResolver(src).PermissionsOf(t.Context(), "admin", uuid.Nil)
	require.NoError(t, err)
	require.Equal(t, authz.Builtin("admin"), got)
}

func TestResolver_CustomRoleKeyIsCappedByItsScopes(t *testing.T) {
	orgID, user, roleID := uuid.New(), uuid.New(), uuid.New()
	src := fakes.NewRoles().SetCustom(orgID, user, roleID, authz.SheetsRead, authz.SheetsWrite, authz.MembersWrite)

	ctx := bindIn(t, src, orgID, session.Principal{KeyOwnerID: user, Source: session.SourceAPIKey, ActiveOrgID: orgID,
		Scopes: []string{authn.ScopeSheetsRead, authn.ScopeSheetsWrite}})
	require.ElementsMatch(t, []authz.Permission{authz.SheetsRead, authz.SheetsWrite}, authz.Held(ctx))
}

func TestResolver_CustomPermissionsFailureFailsClosed(t *testing.T) {
	orgID, user := uuid.New(), uuid.New()
	src := fakes.NewRoles().SetCustom(orgID, user, uuid.New(), authz.SheetsRead)
	_, err := authz.NewResolver(src).Bind(t.Context(), session.Principal{UserID: user, Source: session.SourceOIDC}, tenant.Context{OrgID: orgID})
	require.NoError(t, err)

	missing := fakes.NewRoles()
	missing.Set(orgID, user, "custom")
	_, err = authz.NewResolver(missing).Bind(t.Context(), session.Principal{UserID: user, Source: session.SourceOIDC}, tenant.Context{OrgID: orgID})
	require.NoError(t, err, "custom without a role id holds nothing but still binds")

	broken := fakes.NewRoles().SetCustom(orgID, user, uuid.New(), authz.SheetsRead)
	broken.CustomErr = errors.New("db down")
	_, err = authz.NewResolver(broken).Bind(t.Context(), session.Principal{UserID: user, Source: session.SourceOIDC}, tenant.Context{OrgID: orgID})
	require.True(t, authn.IsUnavailableError(err), "got %T: %v", err, err)
}
