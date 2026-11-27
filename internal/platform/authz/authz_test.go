package authz_test

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/role"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func bindIn(t *testing.T, src *fakes.Roles, orgID uuid.UUID, p session.Principal) context.Context {
	t.Helper()
	ctx, err := authz.NewResolver(src).Bind(context.Background(), p, tenant.Context{OrgID: orgID, UserID: p.UserID})
	require.NoError(t, err)
	return ctx
}

func TestBuiltin_MatrixMatchesSpec(t *testing.T) {
	type row struct{ owner, admin, member bool }
	want := map[authz.Permission]row{
		authz.SheetsRead:               {true, true, true},
		authz.SheetsWrite:              {true, true, true},
		authz.SheetsAdmin:              {true, true, true},
		authz.SheetsEnableWrites:       {true, true, false},
		authz.SpreadsheetsRead:         {true, true, true},
		authz.SpreadsheetsWrite:        {true, true, true},
		authz.SpreadsheetsEnableWrites: {true, true, false},
		authz.CredentialsRead:          {true, true, true},
		authz.CredentialsWrite:         {true, true, true},
		authz.CredentialsManage:        {true, true, false},
		authz.CachePurge:               {true, true, true},
		authz.MembersRead:              {true, true, true},
		authz.APIKeysRead:              {true, true, true},
		authz.ProjectsWrite:            {true, true, false},
		authz.MembersWrite:             {true, true, false},
		authz.APIKeysWrite:             {true, true, false},
		authz.RolesWrite:               {true, false, false},
	}
	defs := authz.Catalogue()
	require.Len(t, defs, len(want), "the catalogue holds exactly the spec's permissions")
	for _, d := range defs {
		r, ok := want[d.Permission]
		require.True(t, ok, "%s is not in the spec matrix", d.Permission)
		assert.Equal(t, r.owner, slices.Contains(authz.Builtin("owner"), d.Permission), "owner %s", d.Permission)
		assert.Equal(t, r.admin, slices.Contains(authz.Builtin("admin"), d.Permission), "admin %s", d.Permission)
		assert.Equal(t, r.member, slices.Contains(authz.Builtin("member"), d.Permission), "member %s", d.Permission)
		assert.NotEmpty(t, d.LabelKey, d.Permission)
	}
	assert.Empty(t, authz.Builtin(""))
	assert.Empty(t, authz.Builtin("stranger"))
}

func TestCatalogue_ScopesAndHolders(t *testing.T) {
	want := map[authz.Permission]string{
		authz.SheetsRead:               authn.ScopeSheetsRead,
		authz.SheetsWrite:              authn.ScopeSheetsWrite,
		authz.SheetsAdmin:              authn.ScopeSheetsAdmin,
		authz.SheetsEnableWrites:       authn.ScopeSheetsAdmin,
		authz.SpreadsheetsRead:         authn.ScopeSpreadsheetsRead,
		authz.SpreadsheetsWrite:        authn.ScopeSpreadsheetsWrite,
		authz.SpreadsheetsEnableWrites: authn.ScopeSpreadsheetsWrite,
		authz.CredentialsRead:          authn.ScopeCredentialsRead,
		authz.CredentialsWrite:         authn.ScopeCredentialsWrite,
		authz.CredentialsManage:        authn.ScopeCredentialsWrite,
		authz.CachePurge:               authn.ScopeCachePurge,
		authz.MembersRead:              authn.ScopeMembersRead,
		authz.APIKeysRead:              authn.ScopeAPIKeysRead,
	}
	for _, d := range authz.Catalogue() {
		scope, ok := want[d.Permission]
		if !ok {
			assert.Equal(t, authz.HolderConsole, d.Holder, "%s is console-only", d.Permission)
			assert.Empty(t, d.Scope, "no scope admits console-only %s", d.Permission)
			continue
		}
		assert.Equal(t, authz.HolderAny, d.Holder, d.Permission)
		assert.Equal(t, scope, d.Scope, d.Permission)
	}
}

func TestCan_RefusesOtherOrgHeldSet(t *testing.T) {
	owner, orgA, orgB := uuid.New(), uuid.New(), uuid.New()
	src := fakes.NewRoles().Set(orgA, owner, "owner").Set(orgB, owner, "owner")
	ctx := bindIn(t, src, orgA, session.Principal{UserID: owner, Source: session.SourceOIDC})
	require.NoError(t, authz.Can(ctx, authz.SheetsRead))

	other := tenant.Into(ctx, tenant.Context{OrgID: orgB, UserID: owner})
	assert.True(t, authz.IsDeniedError(authz.Can(other, authz.SheetsRead)), "a set held in org A answers nothing in org B")
	assert.True(t, authz.IsDeniedError(authz.CanOrgWide(other, authz.MembersWrite)))
}

func TestCan_NoBindRefuses(t *testing.T) {
	assert.True(t, authz.IsDeniedError(authz.Can(context.Background(), authz.SheetsRead)))
	p := session.Principal{UserID: uuid.New(), Source: session.SourceOIDC, Held: []string{string(authz.SheetsRead)}}
	ctx := tenant.Into(session.PrincipalInto(context.Background(), p), tenant.Context{OrgID: uuid.New()})
	assert.True(t, authz.IsDeniedError(authz.Can(ctx, authz.SheetsRead)), "a held set no Bind produced names no org, so it answers nothing")
}

func TestAsSystem_HoldsEverything(t *testing.T) {
	ctx := authz.AsSystem(context.Background())
	for _, d := range authz.Catalogue() {
		require.NoError(t, authz.Can(ctx, d.Permission), d.Permission)
	}
	assert.Len(t, authz.Held(ctx), len(authz.Catalogue()))
}

func TestResolver_TokenCappedByScopes(t *testing.T) {
	owner, orgID := uuid.New(), uuid.New()
	src := fakes.NewRoles().Set(orgID, owner, "owner")
	ctx := bindIn(t, src, orgID, session.Principal{UserID: owner, Source: session.SourceToken, Scopes: []string{authn.ScopeSheetsRead}})

	require.NoError(t, authz.Can(ctx, authz.SheetsRead))
	err := authz.Can(ctx, authz.SheetsWrite)
	de, ok := errors.AsType[*authz.DeniedError](err)
	require.True(t, ok, "want *DeniedError, got %T: %v", err, err)
	assert.Equal(t, authz.SideScope, de.Side)
	assert.Equal(t, []authz.Permission{authz.SheetsRead}, authz.Held(ctx))
}

func TestResolver_TokenCappedByRole(t *testing.T) {
	member, orgID := uuid.New(), uuid.New()
	src := fakes.NewRoles().Set(orgID, member, "member")
	ctx := bindIn(t, src, orgID, session.Principal{UserID: member, Source: session.SourceToken, Scopes: []string{authn.ScopeSheetsAdmin}})

	require.NoError(t, authz.Can(ctx, authz.SheetsAdmin))
	de, ok := errors.AsType[*authz.DeniedError](authz.Can(ctx, authz.SheetsEnableWrites))
	require.True(t, ok)
	assert.Equal(t, authz.SideRole, de.Side)
}

func TestResolver_KeyNeverHoldsConsole(t *testing.T) {
	owner, orgID := uuid.New(), uuid.New()
	src := fakes.NewRoles().Set(orgID, owner, "owner")
	for name, p := range map[string]session.Principal{
		"key of an owner":   {KeyID: uuid.New(), Source: session.SourceAPIKey, ActiveOrgID: orgID, KeyOwnerID: owner, Scopes: authn.AllScopes()},
		"key with no owner": {KeyID: uuid.New(), Source: session.SourceAPIKey, ActiveOrgID: orgID, Scopes: authn.AllScopes()},
		"token of an owner": {UserID: owner, Source: session.SourceToken, Scopes: authn.AllScopes()},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := bindIn(t, src, orgID, p)
			for _, d := range authz.Catalogue() {
				err := authz.Can(ctx, d.Permission)
				if d.Holder == authz.HolderConsole {
					de, ok := errors.AsType[*authz.DeniedError](err)
					require.True(t, ok, "%s must refuse %s", name, d.Permission)
					assert.Equal(t, authz.SideScope, de.Side)
					continue
				}
				require.NoError(t, err, d.Permission)
			}
		})
	}
}

func TestResolver_KeyFollowsCreatorRole(t *testing.T) {
	member, orgID := uuid.New(), uuid.New()
	src := fakes.NewRoles().Set(orgID, member, "member")
	ctx := bindIn(t, src, orgID, session.Principal{KeyID: uuid.New(), Source: session.SourceAPIKey, ActiveOrgID: orgID, KeyOwnerID: member, Scopes: authn.AllScopes()})

	require.NoError(t, authz.Can(ctx, authz.SheetsAdmin))
	assert.True(t, authz.IsDeniedError(authz.Can(ctx, authz.SheetsEnableWrites)), "a key holds no more than its creator's role")
}

func TestResolve_OnceAtBind(t *testing.T) {
	owner, orgID := uuid.New(), uuid.New()
	src := fakes.NewRoles().Set(orgID, owner, "owner")
	ctx := bindIn(t, src, orgID, session.Principal{UserID: owner, Source: session.SourceOIDC})
	for range 3 {
		require.NoError(t, authz.Can(ctx, authz.SheetsWrite))
	}
	assert.Equal(t, 1, src.Calls(), "Can reads the held set; only Bind asks the role source")
}

func TestDeniedError_Codes(t *testing.T) {
	for perm, code := range map[authz.Permission]string{
		authz.MembersWrite:       apperror.CodeOrgManagerRequired,
		authz.APIKeysWrite:       apperror.CodeOrgManagerRequired,
		authz.SheetsEnableWrites: apperror.CodePermissionDenied,
		authz.ProjectsWrite:      apperror.CodePermissionDenied,
		authz.RolesWrite:         apperror.CodePermissionDenied,
	} {
		ae := (&authz.DeniedError{Permission: perm, Org: "Acme", Side: authz.SideRole}).ToAppError()
		assert.Equal(t, code, ae.Code(), perm)
	}
}

func TestDeniedError_MessagesBySide(t *testing.T) {
	member, orgID := uuid.New(), uuid.New()
	src := fakes.NewRoles().Set(orgID, member, "member")

	ctx := bindIn(t, src, orgID, session.Principal{UserID: member, Source: session.SourceOIDC})
	de, ok := errors.AsType[*authz.DeniedError](authz.Can(ctx, authz.SheetsEnableWrites))
	require.True(t, ok)
	assert.Equal(t, "Your role in Acme doesn't allow you to turn on writes for a sheet.", de.ToAppError().Message())

	ctx = bindIn(t, src, orgID, session.Principal{UserID: member, Source: session.SourceToken, Scopes: []string{authn.ScopeSheetsRead}})
	de, ok = errors.AsType[*authz.DeniedError](authz.Can(ctx, authz.SheetsWrite))
	require.True(t, ok)
	assert.Equal(t, "This key or token has no scope that lets it write rows.", de.ToAppError().Message())

	de, ok = errors.AsType[*authz.DeniedError](authz.Can(ctx, authz.MembersWrite))
	require.True(t, ok)
	assert.Equal(t, "Only the web console can manage members.", de.ToAppError().Message())
}

func TestBind_SetsActiveOrgForPerson(t *testing.T) {
	person, orgA, orgB, project := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	src := fakes.NewRoles().Set(orgB, person, "admin")
	for _, source := range []session.Source{session.SourceOIDC, session.SourceToken} {
		p := session.Principal{UserID: person, Source: source, ActiveOrgID: orgA, Scopes: authn.AllScopes()}
		ctx := bindIn(t, src, orgB, p)
		got := session.PrincipalFrom(ctx)
		assert.Equal(t, orgB, got.ActiveOrgID, source)
		assert.Equal(t, orgB, got.HeldOrgID, source)
		assert.Equal(t, "Acme", got.HeldOrg, source)
		assert.Equal(t, "admin", got.HeldRole, source)
		assert.True(t, got.ReachesProject(orgB, project), source)
		tc, err := tenant.From(ctx)
		require.NoError(t, err)
		assert.Equal(t, orgB, tc.OrgID, source)
	}
}

func TestBind_KeyKeepsItsOwnOrg(t *testing.T) {
	owner, keyOrg := uuid.New(), uuid.New()
	src := fakes.NewRoles().Set(keyOrg, owner, "owner")
	ctx := bindIn(t, src, keyOrg, session.Principal{KeyID: uuid.New(), Source: session.SourceAPIKey, ActiveOrgID: keyOrg, KeyOwnerID: owner, AllProjects: true, Scopes: authn.AllScopes()})
	assert.Equal(t, keyOrg, session.PrincipalFrom(ctx).ActiveOrgID)
}

func TestBind_KeyInAnotherOrgIsRefusedWithoutALookup(t *testing.T) {
	owner, keyOrg, otherOrg := uuid.New(), uuid.New(), uuid.New()
	for name, creator := range map[string]uuid.UUID{"creator is a member there": owner, "no recorded creator": uuid.Nil} {
		t.Run(name, func(t *testing.T) {
			src := fakes.NewRoles().Set(keyOrg, owner, "owner").Set(otherOrg, owner, "owner")
			p := session.Principal{KeyID: uuid.New(), Source: session.SourceAPIKey, ActiveOrgID: keyOrg, KeyOwnerID: creator, AllProjects: true, Scopes: authn.AllScopes()}
			_, err := authz.NewResolver(src).Bind(context.Background(), p, tenant.Context{OrgID: otherOrg})
			assert.True(t, authn.IsUnauthorizedError(err), "want *authn.UnauthorizedError, got %T: %v", err, err)
			assert.Zero(t, src.Calls(), "no role lookup in an org the key cannot reach")
		})
	}
}

func TestBind_NonMemberIsUnauthorized(t *testing.T) {
	stranger, orgID := uuid.New(), uuid.New()
	src := fakes.NewRoles()
	for name, p := range map[string]session.Principal{
		"person":         {UserID: stranger, Source: session.SourceOIDC},
		"token":          {UserID: stranger, Source: session.SourceToken, Scopes: authn.AllScopes()},
		"key of creator": {KeyID: uuid.New(), Source: session.SourceAPIKey, ActiveOrgID: orgID, KeyOwnerID: stranger, Scopes: authn.AllScopes()},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := authz.NewResolver(src).Bind(context.Background(), p, tenant.Context{OrgID: orgID})
			assert.True(t, authn.IsUnauthorizedError(err), "want *authn.UnauthorizedError, got %T: %v", err, err)
		})
	}
}

func TestBind_RoleSourceFailureIsNotUnauthorized(t *testing.T) {
	person, orgID := uuid.New(), uuid.New()
	src := fakes.NewRoles()
	src.Err = errors.New("store down")
	_, err := authz.NewResolver(src).Bind(context.Background(), session.Principal{UserID: person, Source: session.SourceOIDC}, tenant.Context{OrgID: orgID})
	require.Error(t, err)
	assert.False(t, authn.IsUnauthorizedError(err), "an outage must not read as a refusal")
	assert.True(t, authn.IsUnavailableError(err), "want *authn.UnavailableError, got %T: %v", err, err)
	assert.ErrorIs(t, err, src.Err)
}

func TestAuthz_ImportsNoDomainPackage(t *testing.T) {
	domain := regexp.MustCompile(`^altalune\.id/opensheet/internal/(org|apikey|project|invite|sheet|spreadsheet|credential|role)(/|$)`)
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	parsed := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		require.NoError(t, err)
		parsed++
		for _, imp := range af.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			require.NoError(t, err)
			assert.False(t, domain.MatchString(path), "%s imports domain package %s", f, path)
		}
	}
	require.NotZero(t, parsed, "parsed no source files")
}

func TestPermissionFor_ReturnsBasePermission(t *testing.T) {
	for scope, want := range map[string]authz.Permission{
		authn.ScopeSheetsRead:        authz.SheetsRead,
		authn.ScopeSheetsWrite:       authz.SheetsWrite,
		authn.ScopeSheetsAdmin:       authz.SheetsAdmin,
		authn.ScopeSpreadsheetsRead:  authz.SpreadsheetsRead,
		authn.ScopeSpreadsheetsWrite: authz.SpreadsheetsWrite,
		authn.ScopeCredentialsRead:   authz.CredentialsRead,
		authn.ScopeCredentialsWrite:  authz.CredentialsWrite,
		authn.ScopeCachePurge:        authz.CachePurge,
		authn.ScopeMembersRead:       authz.MembersRead,
		authn.ScopeAPIKeysRead:       authz.APIKeysRead,
	} {
		got, ok := authz.PermissionFor(scope)
		require.True(t, ok, scope)
		assert.Equal(t, want, got, scope)
	}
	for _, scope := range []string{"", "nope:read", authn.ScopeAPIKeysWrite} {
		_, ok := authz.PermissionFor(scope)
		assert.False(t, ok, scope)
	}
}

func TestErrorCodes_ROLRegistered(t *testing.T) {
	codesSrc, err := os.ReadFile(filepath.Join("..", "..", "apperror", "codes.go"))
	require.NoError(t, err)
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "errors", "opensheet.md"))
	require.NoError(t, err)

	registered := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\tCode\w+\s+=\s+"(ROL\d{3})"`).FindAllStringSubmatch(string(codesSrc), -1) {
		registered[m[1]] = true
	}
	require.NotEmpty(t, registered, "no ROL constant in codes.go")

	built := map[string]*apperror.AppError{}
	for _, e := range []interface{ ToAppError() *apperror.AppError }{
		&authz.DeniedError{Permission: authz.SheetsEnableWrites, Org: "Acme", Side: authz.SideRole},
		&authz.EscalationError{Role: "owner"},
		&authz.EscalationError{Role: "owner", Op: authz.EscalationRemove},
		&authz.LastOwnerError{Org: "Acme"},
		&role.NotFoundError{ID: "r1"},
		&role.InUseError{ID: "r1"},
		&role.InvalidPermissionError{Permission: "nope"},
		&role.SystemOrgError{},
		&role.InvalidNameError{},
		&role.AlreadyExistsError{Name: "Clerk"},
		&role.StaleVersionError{Want: 1, Got: 2},
	} {
		ae := e.ToAppError()
		built[ae.Code()] = ae
	}
	for code := range registered {
		assert.Regexp(t, "(?m)^\\|\\s*`"+code+"`\\s*\\|", string(doc), "%s has no row in docs/errors/opensheet.md", code)
		ae, ok := built[code]
		if !assert.True(t, ok, "no error constructs %s", code) {
			continue
		}
		assert.NotEmpty(t, ae.Message(), code)
		assert.NotContains(t, ae.Message(), "authz.", "%s message is an untranslated key", code)
	}
}
