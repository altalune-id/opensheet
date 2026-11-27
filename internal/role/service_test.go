package role_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/role"
	"altalune.id/opensheet/internal/testutil/fakes"
)

type staffOrgs map[uuid.UUID]bool

func (s staffOrgs) IsStaff(_ context.Context, orgID uuid.UUID) (bool, error) { return s[orgID], nil }

type serviceFixture struct {
	svc   *role.Service
	store *fakes.Role
	orgID uuid.UUID
	roles *fakes.Roles
}

func newServiceFixture(t *testing.T, staff bool) serviceFixture {
	t.Helper()
	orgID := uuid.New()
	store := fakes.NewRole()
	log := slog.New(slog.DiscardHandler)
	svc := role.NewService(store, staffOrgs{orgID: staff}, log, apperror.NewReporter(log, false).Unexpected, fakes.UnitOfWork)
	return serviceFixture{svc: svc, store: store, orgID: orgID, roles: fakes.NewRoles()}
}

func (f serviceFixture) bind(t *testing.T, userID uuid.UUID) context.Context {
	t.Helper()
	ctx, err := authz.NewResolver(f.roles).Bind(t.Context(),
		session.Principal{UserID: userID, Source: session.SourceOIDC},
		tenant.Context{OrgID: f.orgID, UserID: userID})
	require.NoError(t, err)
	return ctx
}

func (f serviceFixture) as(t *testing.T, builtin string) context.Context {
	t.Helper()
	u := uuid.New()
	f.roles.Set(f.orgID, u, builtin)
	return f.bind(t, u)
}

func (f serviceFixture) asCustom(t *testing.T, perms ...authz.Permission) context.Context {
	t.Helper()
	u := uuid.New()
	f.roles.SetCustom(f.orgID, u, uuid.New(), perms...)
	return f.bind(t, u)
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	ae, ok := apperror.AsAppError(err)
	require.True(t, ok, "want an AppError, got %T: %v", err, err)
	require.Equal(t, code, ae.Code())
}

func TestRole_CreateNeedsRolesWrite(t *testing.T) {
	f := newServiceFixture(t, false)

	_, err := f.svc.Create(f.as(t, "admin"), "Clerk", "", []authz.Permission{authz.SheetsRead})
	require.True(t, authz.IsDeniedError(err), "got %T: %v", err, err)
	requireCode(t, err, apperror.CodePermissionDenied)

	r, err := f.svc.Create(f.as(t, "owner"), "Clerk", "reads sheets", []authz.Permission{authz.SheetsRead})
	require.NoError(t, err)
	got, err := f.store.ByID(t.Context(), r.ID)
	require.NoError(t, err)
	require.Equal(t, []authz.Permission{authz.SheetsRead}, got.Permissions)
	require.Equal(t, f.orgID, got.OrgID)
}

func TestRole_CannotExceedEditor(t *testing.T) {
	f := newServiceFixture(t, false)
	editor := f.asCustom(t, authz.RolesWrite, authz.MembersWrite, authz.SheetsRead)

	_, err := f.svc.Create(editor, "Writer", "", []authz.Permission{authz.SheetsRead, authz.SheetsWrite})
	require.True(t, authz.IsEscalationError(err), "got %T: %v", err, err)
	requireCode(t, err, apperror.CodeRoleEscalation)

	r, err := f.svc.Create(editor, "Reader", "", []authz.Permission{authz.SheetsRead, authz.RolesWrite})
	require.NoError(t, err, "a custom role may hold roles:write within its editor")

	_, err = f.svc.Update(editor, r.ID, "Reader", "", []authz.Permission{authz.SheetsWrite}, 0)
	requireCode(t, err, apperror.CodeRoleEscalation)

	wide, err := f.svc.Create(f.as(t, "owner"), "Wide", "", []authz.Permission{authz.SheetsRead, authz.SheetsWrite})
	require.NoError(t, err)
	_, err = f.svc.Update(editor, wide.ID, "Wide", "", []authz.Permission{authz.SheetsRead}, 0)
	require.True(t, authz.IsEscalationError(err), "an editor may not strip a permission it does not hold, got %T: %v", err, err)
	got, err := f.store.ByID(t.Context(), wide.ID)
	require.NoError(t, err)
	require.Equal(t, []authz.Permission{authz.SheetsRead, authz.SheetsWrite}, got.Permissions)
}

func TestRole_UnknownPermissionIsROL005(t *testing.T) {
	f := newServiceFixture(t, false)
	for _, p := range []authz.Permission{"sheets:*", "nope", ""} {
		_, err := f.svc.Create(f.as(t, "owner"), "Odd", "", []authz.Permission{authz.SheetsRead, p})
		requireCode(t, err, apperror.CodeRoleInvalidPermission)
	}
}

func TestRole_StaffOrgOffersNoCustomRoles(t *testing.T) {
	f := newServiceFixture(t, true)
	r, err := role.New(f.orgID, "Clerk", "", []authz.Permission{authz.SheetsRead}, time.Now())
	require.NoError(t, err)
	require.NoError(t, f.store.Save(t.Context(), r, 0))
	ctx := f.as(t, "owner")

	allowed, err := f.svc.Allowed(ctx)
	require.NoError(t, err)
	require.False(t, allowed)
	items, err := f.svc.List(ctx)
	require.NoError(t, err)
	require.Empty(t, items, "the staff org's role pickers list built-ins only")

	g := newServiceFixture(t, false)
	allowed, err = g.svc.Allowed(g.as(t, "owner"))
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestRole_SystemOrgIsROL006(t *testing.T) {
	f := newServiceFixture(t, true)
	_, err := f.svc.Create(f.as(t, "owner"), "Clerk", "", []authz.Permission{authz.SheetsRead})
	require.True(t, role.IsSystemOrgError(err), "got %T: %v", err, err)
	requireCode(t, err, apperror.CodeRoleSystemOrg)
}

func TestRole_NameRules(t *testing.T) {
	f := newServiceFixture(t, false)
	owner := f.as(t, "owner")
	for _, name := range []string{"", "   ", strings.Repeat("x", role.MaxNameLen+1)} {
		_, err := f.svc.Create(owner, name, "", []authz.Permission{authz.SheetsRead})
		requireCode(t, err, apperror.CodeRoleInvalidName)
	}
	_, err := f.svc.Create(owner, strings.Repeat("x", role.MaxNameLen), "", nil)
	require.NoError(t, err)

	_, err = f.svc.Create(owner, "Clerk", "", nil)
	require.NoError(t, err)
	_, err = f.svc.Create(owner, "clerk", "", nil)
	requireCode(t, err, apperror.CodeRoleAlreadyExists)
}

func TestRole_UpdateIfVersion(t *testing.T) {
	f := newServiceFixture(t, false)
	owner := f.as(t, "owner")
	r, err := f.svc.Create(owner, "Clerk", "", []authz.Permission{authz.SheetsRead})
	require.NoError(t, err)

	got, err := f.svc.Update(owner, r.ID, "Clerk", "", []authz.Permission{authz.SheetsRead, authz.SheetsWrite}, r.Version)
	require.NoError(t, err)
	require.Equal(t, r.Version+1, got.Version)

	_, err = f.svc.Update(owner, r.ID, "Clerk", "", nil, r.Version)
	requireCode(t, err, apperror.CodeRoleStaleVersion)
}

func TestRole_ReadsNeedMembersWrite(t *testing.T) {
	f := newServiceFixture(t, false)
	member := f.as(t, "member")
	_, err := f.svc.List(member)
	require.True(t, authz.IsDeniedError(err), "got %T: %v", err, err)
	_, err = f.svc.HeldBy(member)
	require.True(t, authz.IsDeniedError(err), "got %T: %v", err, err)
	_, err = f.svc.Get(member, uuid.New())
	require.True(t, authz.IsDeniedError(err), "got %T: %v", err, err)

	_, err = f.svc.List(f.as(t, "admin"))
	require.NoError(t, err)
}

func TestRole_DeleteNeedsRolesWriteAndRefusesHeld(t *testing.T) {
	f := newServiceFixture(t, false)
	owner := f.as(t, "owner")
	r, err := f.svc.Create(owner, "Clerk", "", nil)
	require.NoError(t, err)

	require.True(t, authz.IsDeniedError(f.svc.Delete(f.as(t, "admin"), r.ID)))
	f.store.Hold(r.ID)
	requireCode(t, f.svc.Delete(owner, r.ID), apperror.CodeRoleInUse)
	f.store.Release(r.ID)
	require.NoError(t, f.svc.Delete(owner, r.ID))
}

type racingStore struct {
	*fakes.Role
	race func(ctx context.Context, r *role.Role)
}

func (s racingStore) ByID(ctx context.Context, id uuid.UUID) (*role.Role, error) {
	r, err := s.Role.ByID(ctx, id)
	if err == nil && s.race != nil {
		s.race(ctx, r)
	}
	return r, err
}

func TestRole_UpdateWithoutIfVersionKeepsConcurrentEdit(t *testing.T) {
	f := newServiceFixture(t, false)
	ctx := f.as(t, "owner")
	r, err := f.svc.Create(ctx, "Clerk", "", []authz.Permission{authz.SheetsRead})
	require.NoError(t, err)

	store := racingStore{Role: f.store, race: func(ctx context.Context, loaded *role.Role) {
		other := *loaded
		other.Name = "Concurrent"
		require.NoError(t, f.store.Save(ctx, &other, 0))
	}}
	log := slog.New(slog.DiscardHandler)
	svc := role.NewService(store, staffOrgs{f.orgID: false}, log, apperror.NewReporter(log, false).Unexpected, fakes.UnitOfWork)

	_, err = svc.Update(ctx, r.ID, "Mine", "", []authz.Permission{authz.SheetsRead}, 0)
	require.True(t, role.IsStaleVersionError(err), "got %T: %v", err, err)
	got, err := f.store.ByID(t.Context(), r.ID)
	require.NoError(t, err)
	require.Equal(t, "Concurrent", got.Name)
}
