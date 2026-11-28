package handlers

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func TestSignIn_DefaultProjectInsideGrant(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	unexpected := func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		return apperror.New("x", msg, 0).WithCause(cause)
	}
	orgStore, projStore := fakes.NewOrg(), fakes.NewProject()
	orgs := org.NewService(orgStore, capabilities.Capabilities{}, log, unexpected, fakes.UnitOfWork)
	projects := project.NewService(projStore, log, unexpected)

	owner, member := uuid.New(), uuid.New()
	o, err := org.NewOrg("acme", "Acme", owner)
	require.NoError(t, err)
	require.NoError(t, orgStore.Save(ctx, o))
	m, err := org.NewMembership(o.ID, member, org.RoleMember)
	require.NoError(t, err)
	require.NoError(t, orgStore.SaveMembership(ctx, m))
	first, err := project.New(o.ID, "first", "First")
	require.NoError(t, err)
	require.NoError(t, projStore.Save(ctx, first))
	granted, err := project.New(o.ID, "granted", "Granted")
	require.NoError(t, err)
	require.NoError(t, projStore.Save(ctx, granted))
	require.NoError(t, orgStore.SetProjectGrant(tenant.WithOrg(ctx, o.ID), o.ID, member, org.ProjectGrant{ProjectIDs: []uuid.UUID{granted.ID}}))

	h := &AuthHandler{Deps: Deps{Authz: authz.NewResolver(fakes.OrgRoles{Orgs: orgs})}, Orgs: orgs, Projects: projects}

	got := h.resolveActiveTenant(ctx, session.Principal{UserID: member, Source: session.SourceOIDC})
	require.Equal(t, o.ID, got.ActiveOrgID)
	require.Equal(t, granted.ID, got.ActiveProjectID, "a fresh sign-in lands in a project the grant reaches")

	got = h.resolveActiveTenant(ctx, session.Principal{UserID: member, Source: session.SourceOIDC, ActiveOrgID: o.ID, ActiveProjectID: first.ID})
	require.Equal(t, granted.ID, got.ActiveProjectID, "a remembered project outside the grant is treated as unset")
}
