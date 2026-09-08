package apikey_test

import (
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
)

type usageRecord struct {
	orgID     uuid.UUID
	projectID uuid.UUID
	keyID     uuid.UUID
}

type countingUsage struct {
	mu   sync.Mutex
	seen []usageRecord
}

func (c *countingUsage) Record(orgID, projectID, keyID uuid.UUID, _ time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, usageRecord{orgID: orgID, projectID: projectID, keyID: keyID})
}

func (c *countingUsage) records() []usageRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.seen)
}

func (h *harness) mintGranted(t *testing.T, scopes []string, sheetIDs []uuid.UUID) (*apikey.APIKey, string) {
	t.Helper()
	for _, id := range sheetIDs {
		h.sheets.Add(h.tc.OrgID, h.tc.ProjectID, id)
	}
	k, plaintext, err := h.svc.Create(h.ctx, apikey.CreateRequest{Name: "ci", Scopes: scopes, SheetIDs: sheetIDs})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return k, plaintext
}

// TestAuthenticator_RejectsForeignShapesWithoutTouchingTheStore pins spec 6.2's contract on every chain member:
// shape dispatch belongs to the authenticator, so a JWT must cost no database lookup.
func TestAuthenticator_RejectsForeignShapesWithoutTouchingTheStore(t *testing.T) {
	h := newHarness(t)
	boom := errors.New("store must not be consulted")
	h.store.StickyError = true
	h.store.SaveErr = boom
	h.store.ByIDErr = boom
	h.store.ByPrefixErr = boom
	h.store.ListErr = boom
	h.store.DeleteErr = boom
	h.store.TouchLastUsedErr = boom

	usage := &countingUsage{}
	a := apikey.NewAuthenticator(h.svc, usage)

	for _, raw := range []string{
		"eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiJ4In0.sig",
		"", "garbage", "Bearer osk_x",
	} {
		if _, err := a.Authenticate(t.Context(), raw); !apikey.IsUnauthorizedError(err) {
			t.Errorf("Authenticate(%q) error = %v, want *UnauthorizedError", raw, err)
		}
		_, err := a.Authorize(t.Context(), raw, authn.ScopeSheetsRead, h.tc.OrgID, h.tc.ProjectID, uuid.Must(uuid.NewV7()))
		if !apikey.IsUnauthorizedError(err) {
			t.Errorf("Authorize(%q) error = %v, want *UnauthorizedError", raw, err)
		}
	}
	if *h.unex != 0 {
		t.Errorf("unexpected() called %d times, want 0 — the store was consulted for a foreign shape", *h.unex)
	}
	if got := len(usage.records()); got != 0 {
		t.Errorf("usage recorded %d times for rejected credentials, want 0", got)
	}
}

func TestAuthenticator_PrincipalCarriesScopeAndTenantButNoSecret(t *testing.T) {
	h := newHarness(t)
	usage := &countingUsage{}
	a := apikey.NewAuthenticator(h.svc, usage)
	k, plaintext := h.mint(t, authn.ScopeSheetsRead, authn.ScopeCachePurge)

	p, err := a.Authenticate(t.Context(), plaintext)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if p.UserID != uuid.Nil {
		t.Errorf("UserID = %v, want uuid.Nil — a key is not a person", p.UserID)
	}
	if p.Source != session.SourceAPIKey {
		t.Errorf("Source = %q, want %q", p.Source, session.SourceAPIKey)
	}
	if p.ActiveOrgID != k.OrgID || p.ActiveProjectID != k.ProjectID {
		t.Errorf("tenant = (%v, %v), want (%v, %v)", p.ActiveOrgID, p.ActiveProjectID, k.OrgID, k.ProjectID)
	}
	if !slices.Equal(p.Scopes, k.Scopes) {
		t.Errorf("Scopes = %v, want %v", p.Scopes, k.Scopes)
	}

	_, secret, err := apikey.Parse(plaintext)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rendered := fmt.Sprintf("%#v %+v", p, p)
	for name, needle := range map[string]string{
		"plaintext":   plaintext,
		"secret half": secret,
		"secret hash": hex.EncodeToString(k.SecretHash),
	} {
		if strings.Contains(rendered, needle) {
			t.Errorf("the principal renders the %s", name)
		}
	}

	want := []usageRecord{{orgID: k.OrgID, projectID: k.ProjectID, keyID: k.ID}}
	if got := usage.records(); !slices.Equal(got, want) {
		t.Errorf("usage records = %v, want %v", got, want)
	}
}

// TestAuthorize_RefusesASheetOutsideTheGrant is the whole reason Authorize exists: a session.Principal
// cannot carry SheetIDs, so an earlier design draft would have made every sheet-restricted key project-wide.
func TestAuthorize_RefusesASheetOutsideTheGrant(t *testing.T) {
	h := newHarness(t)
	usage := &countingUsage{}
	a := apikey.NewAuthenticator(h.svc, usage)
	sheetA, sheetB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	k, plaintext := h.mintGranted(t, []string{authn.ScopeSheetsRead}, []uuid.UUID{sheetA})

	p, err := a.Authorize(t.Context(), plaintext, authn.ScopeSheetsRead, k.OrgID, k.ProjectID, sheetA)
	if err != nil {
		t.Fatalf("Authorize on the granted sheet: %v", err)
	}
	if p.ActiveProjectID != k.ProjectID {
		t.Errorf("ActiveProjectID = %v, want %v", p.ActiveProjectID, k.ProjectID)
	}

	_, errB := a.Authorize(t.Context(), plaintext, authn.ScopeSheetsRead, k.OrgID, k.ProjectID, sheetB)
	if !apikey.IsUnauthorizedError(errB) {
		t.Fatalf("Authorize on an ungranted sheet = %v, want *UnauthorizedError", errB)
	}
	_, errScope := a.Authorize(t.Context(), plaintext, authn.ScopeSheetsWrite, k.OrgID, k.ProjectID, sheetA)
	if !apikey.IsUnauthorizedError(errScope) {
		t.Fatalf("Authorize with a scope the key lacks = %v, want *UnauthorizedError", errScope)
	}
	if errB.Error() != errScope.Error() {
		t.Errorf("sheet rejection %q differs from scope rejection %q", errB.Error(), errScope.Error())
	}
	if got := len(usage.records()); got != 3 {
		t.Errorf("usage records = %d, want 3 — a resolved key was used even when authorization failed", got)
	}
}

func TestAuthorize_EmptyGrantCoversItsOwnProjectOnly(t *testing.T) {
	h := newHarness(t)
	a := apikey.NewAuthenticator(h.svc, &countingUsage{})
	k, plaintext := h.mint(t, authn.ScopeSheetsRead)

	for range 3 {
		if _, err := a.Authorize(t.Context(), plaintext, authn.ScopeSheetsRead,
			k.OrgID, k.ProjectID, uuid.Must(uuid.NewV7())); err != nil {
			t.Fatalf("Authorize with an empty grant inside the key's own project: %v", err)
		}
	}

	cases := map[string]struct{ orgID, projectID uuid.UUID }{
		"another org":     {orgID: uuid.Must(uuid.NewV7()), projectID: k.ProjectID},
		"another project": {orgID: k.OrgID, projectID: uuid.Must(uuid.NewV7())},
		"both foreign":    {orgID: uuid.Must(uuid.NewV7()), projectID: uuid.Must(uuid.NewV7())},
	}
	for name, c := range cases {
		_, err := a.Authorize(t.Context(), plaintext, authn.ScopeSheetsRead, c.orgID, c.projectID, uuid.Must(uuid.NewV7()))
		if !apikey.IsUnauthorizedError(err) {
			t.Errorf("Authorize under %s = %v, want *UnauthorizedError", name, err)
		}
	}
}

// TestAuthenticator_EveryFailurePathIsByteIdentical extends the service's guarantee across the two new
// seams: telling a caller which of the eight rejections fired is the leak.
func TestAuthenticator_EveryFailurePathIsByteIdentical(t *testing.T) {
	collect := func(t *testing.T, name string, fn func(h *harness, a *apikey.Authenticator) error) (string, string) {
		t.Helper()
		h := newHarness(t)
		err := fn(h, apikey.NewAuthenticator(h.svc, &countingUsage{}))
		if err == nil {
			t.Fatalf("%s: want an error", name)
		}
		if !apikey.IsUnauthorizedError(err) {
			t.Fatalf("%s: err = %#v, want *UnauthorizedError", name, err)
		}
		app, ok := apperror.AsAppError(err)
		if !ok {
			t.Fatalf("%s: err does not convert to an AppError", name)
		}
		return err.Error(), app.Code()
	}

	paths := []struct {
		name string
		fn   func(h *harness, a *apikey.Authenticator) error
	}{
		{name: "missing", fn: func(_ *harness, a *apikey.Authenticator) error {
			_, err := a.Authenticate(t.Context(), "")
			return err
		}},
		{name: "foreign shape", fn: func(_ *harness, a *apikey.Authenticator) error {
			_, err := a.Authenticate(t.Context(), "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiJ4In0.sig")
			return err
		}},
		{name: "malformed", fn: func(_ *harness, a *apikey.Authenticator) error {
			_, err := a.Authenticate(t.Context(), "osk_nope_nope")
			return err
		}},
		{name: "unknown", fn: func(h *harness, a *apikey.Authenticator) error {
			k, plaintext := h.mint(t)
			if dErr := h.store.Delete(h.ctx, k.ID); dErr != nil {
				t.Fatalf("Delete: %v", dErr)
			}
			_, err := a.Authenticate(t.Context(), plaintext)
			return err
		}},
		{name: "revoked", fn: func(h *harness, a *apikey.Authenticator) error {
			k, plaintext := h.mint(t)
			if _, rErr := h.svc.Revoke(h.ctx, k.ID); rErr != nil {
				t.Fatalf("Revoke: %v", rErr)
			}
			_, err := a.Authenticate(t.Context(), plaintext)
			return err
		}},
		{name: "expired", fn: func(h *harness, a *apikey.Authenticator) error {
			k, plaintext := h.mint(t)
			expired := time.Now().UTC().Add(-time.Hour)
			k.ExpiresAt = &expired
			if sErr := h.store.Save(h.ctx, k); sErr != nil {
				t.Fatalf("Save: %v", sErr)
			}
			_, err := a.Authenticate(t.Context(), plaintext)
			return err
		}},
		{name: "store failure", fn: func(h *harness, a *apikey.Authenticator) error {
			_, plaintext := h.mint(t)
			h.store.StickyError = true
			h.store.ByPrefixErr = errors.New("connection refused")
			_, err := a.Authenticate(t.Context(), plaintext)
			return err
		}},
		{name: "wrong org", fn: func(h *harness, a *apikey.Authenticator) error {
			k, plaintext := h.mint(t)
			_, err := a.Authorize(t.Context(), plaintext, authn.ScopeSheetsRead,
				uuid.Must(uuid.NewV7()), k.ProjectID, uuid.Must(uuid.NewV7()))
			return err
		}},
		{name: "wrong project", fn: func(h *harness, a *apikey.Authenticator) error {
			k, plaintext := h.mint(t)
			_, err := a.Authorize(t.Context(), plaintext, authn.ScopeSheetsRead,
				k.OrgID, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()))
			return err
		}},
		{name: "wrong sheet", fn: func(h *harness, a *apikey.Authenticator) error {
			sheetA := uuid.Must(uuid.NewV7())
			k, plaintext := h.mintGranted(t, []string{authn.ScopeSheetsRead}, []uuid.UUID{sheetA})
			_, err := a.Authorize(t.Context(), plaintext, authn.ScopeSheetsRead,
				k.OrgID, k.ProjectID, uuid.Must(uuid.NewV7()))
			return err
		}},
		{name: "missing scope", fn: func(h *harness, a *apikey.Authenticator) error {
			k, plaintext := h.mint(t, authn.ScopeSheetsRead)
			_, err := a.Authorize(t.Context(), plaintext, authn.ScopeCachePurge,
				k.OrgID, k.ProjectID, uuid.Must(uuid.NewV7()))
			return err
		}},
	}

	var messages, codesSeen []string
	for _, p := range paths {
		msg, code := collect(t, p.name, p.fn)
		messages = append(messages, msg)
		codesSeen = append(codesSeen, code)
	}
	for i := range messages {
		if messages[i] != messages[0] {
			t.Errorf("%s message = %q, want %q — the rejection is distinguishable",
				paths[i].name, messages[i], messages[0])
		}
		if codesSeen[i] != apperror.CodeAPIKeyUnauthorized {
			t.Errorf("%s code = %q, want KEY004", paths[i].name, codesSeen[i])
		}
	}
}

func TestNewAuthenticator_WithoutARecorderStillAuthenticates(t *testing.T) {
	h := newHarness(t)
	a := apikey.NewAuthenticator(h.svc, nil)
	_, plaintext := h.mint(t)

	if _, err := a.Authenticate(t.Context(), plaintext); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
}

// TestAuthorizeProject_RefusesASheetRestrictedKey pins the documented rule for routes that name no
// sheet: a key granted specific sheets cannot use them at all, whatever scopes it holds.
func TestAuthorizeProject_RefusesASheetRestrictedKey(t *testing.T) {
	h := newHarness(t)
	a := apikey.NewAuthenticator(h.svc, &countingUsage{})
	granted := uuid.Must(uuid.NewV7())
	restricted, restrictedPlain := h.mintGranted(t,
		[]string{authn.ScopeSpreadsheetsRead, authn.ScopeSpreadsheetsWrite}, []uuid.UUID{granted})
	open, openPlain := h.mintGranted(t, []string{authn.ScopeSpreadsheetsRead}, nil)

	p, err := a.AuthorizeProject(t.Context(), openPlain, authn.ScopeSpreadsheetsRead, open.OrgID, open.ProjectID)
	if err != nil {
		t.Fatalf("AuthorizeProject with an unrestricted key: %v", err)
	}
	if p.ActiveProjectID != open.ProjectID {
		t.Errorf("ActiveProjectID = %v, want %v", p.ActiveProjectID, open.ProjectID)
	}

	_, err = a.AuthorizeProject(t.Context(), restrictedPlain, authn.ScopeSpreadsheetsRead,
		restricted.OrgID, restricted.ProjectID)
	if !apikey.IsUnauthorizedError(err) {
		t.Fatalf("AuthorizeProject with a sheet-restricted key = %v, want *UnauthorizedError", err)
	}

	_, err = a.AuthorizeProject(t.Context(), openPlain, authn.ScopeSpreadsheetsWrite, open.OrgID, open.ProjectID)
	if !apikey.IsUnauthorizedError(err) {
		t.Fatalf("AuthorizeProject with a scope the key lacks = %v, want *UnauthorizedError", err)
	}

	for name, c := range map[string]struct{ orgID, projectID uuid.UUID }{
		"another org":     {orgID: uuid.Must(uuid.NewV7()), projectID: open.ProjectID},
		"another project": {orgID: open.OrgID, projectID: uuid.Must(uuid.NewV7())},
	} {
		_, fErr := a.AuthorizeProject(t.Context(), openPlain, authn.ScopeSpreadsheetsRead, c.orgID, c.projectID)
		if !apikey.IsUnauthorizedError(fErr) {
			t.Errorf("AuthorizeProject under %s = %v, want *UnauthorizedError", name, fErr)
		}
	}
}

func TestAPIKey_AllowsProject(t *testing.T) {
	sheetID := uuid.Must(uuid.NewV7())
	cases := map[string]struct {
		key   apikey.APIKey
		scope string
		want  bool
	}{
		"unrestricted key with the scope": {
			key:   apikey.APIKey{Scopes: []string{authn.ScopeSpreadsheetsRead}},
			scope: authn.ScopeSpreadsheetsRead,
			want:  true,
		},
		"unrestricted key without the scope": {
			key:   apikey.APIKey{Scopes: []string{authn.ScopeSheetsRead}},
			scope: authn.ScopeSpreadsheetsRead,
			want:  false,
		},
		"sheet-restricted key with the scope": {
			key:   apikey.APIKey{Scopes: []string{authn.ScopeSpreadsheetsRead}, SheetIDs: []uuid.UUID{sheetID}},
			scope: authn.ScopeSpreadsheetsRead,
			want:  false,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := c.key.AllowsProject(c.scope); got != c.want {
				t.Errorf("AllowsProject(%q) = %v, want %v", c.scope, got, c.want)
			}
		})
	}
}
