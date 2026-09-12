package api_test

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/types/known/timestamppb"

	apikeyv1 "altalune.id/opensheet/gen/go/apikey/v1"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
)

func TestAPIKey_CreateAndList_HappyPath(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)
	client := h.apiKeyClient()
	expires := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)

	create := connect.NewRequest(&apikeyv1.CreateRequest{
		ProjectId: proj.ID.String(),
		Name:      "ci",
		Scopes:    []string{authn.ScopeSheetsRead},
		ExpiresAt: timestamppb.New(expires),
	})
	withBearer(create.Header())
	createResp, err := client.Create(t.Context(), create)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	made := createResp.Msg.GetApiKey()
	if made.GetName() != "ci" || made.GetProjectId() != proj.ID.String() {
		t.Errorf("created = %+v", made)
	}
	if got := made.GetScopes(); len(got) != 1 || got[0] != authn.ScopeSheetsRead {
		t.Errorf("scopes = %v", got)
	}
	if !made.GetExpiresAt().AsTime().Equal(expires) {
		t.Errorf("expires_at = %v, want %v", made.GetExpiresAt().AsTime(), expires)
	}
	plaintext := createResp.Msg.GetPlaintextKey()
	if !strings.HasPrefix(plaintext, apikey.Label+"_") {
		t.Fatalf("plaintext_key = %q, want an %s_ prefix", plaintext, apikey.Label)
	}
	if made.GetKeyPrefix() == "" || !strings.Contains(plaintext, made.GetKeyPrefix()) {
		t.Errorf("key_prefix %q is not the public half of %q", made.GetKeyPrefix(), plaintext)
	}

	list := connect.NewRequest(&apikeyv1.ListRequest{ProjectId: proj.ID.String()})
	withBearer(list.Header())
	listResp, err := client.List(t.Context(), list)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if n := len(listResp.Msg.GetApiKeys()); n != 1 {
		t.Fatalf("api_keys len = %d, want 1", n)
	}
}

// SECURITY: the plaintext is returned by Create exactly once; List must expose neither it nor the hash.
func TestAPIKey_ListReturnsNeitherPlaintextNorHash(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)
	client := h.apiKeyClient()

	create := connect.NewRequest(&apikeyv1.CreateRequest{
		ProjectId: proj.ID.String(),
		Name:      "ci",
		Scopes:    []string{authn.ScopeSheetsRead},
	})
	withBearer(create.Header())
	createResp, err := client.Create(t.Context(), create)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	plaintext := createResp.Msg.GetPlaintextKey()
	if plaintext == "" {
		t.Fatal("Create returned no plaintext, so the leak assertion would be vacuous")
	}
	secret := strings.TrimPrefix(plaintext, apikey.Label+"_"+createResp.Msg.GetApiKey().GetKeyPrefix()+"_")

	stored, err := h.keys.List(t.Context(), orgID, proj.ID)
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored = %d keys, err %v", len(stored), err)
	}

	list := connect.NewRequest(&apikeyv1.ListRequest{ProjectId: proj.ID.String()})
	withBearer(list.Header())
	listResp, err := client.List(t.Context(), list)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	wire := prototext.Format(listResp.Msg)
	for _, needle := range []string{plaintext, secret, hex.EncodeToString(stored[0].SecretHash)} {
		if needle != "" && strings.Contains(wire, needle) {
			t.Errorf("ListResponse leaks %q", needle)
		}
	}

	fields := apikeyv1.File_apikey_v1_apikey_proto.Messages().ByName("APIKey").Fields()
	for i := range fields.Len() {
		n := string(fields.Get(i).Name())
		if strings.Contains(n, "secret") || strings.Contains(n, "hash") || strings.Contains(n, "plaintext") {
			t.Errorf("APIKey proto declares a %q field", n)
		}
	}
}

func TestAPIKey_Revoke_HappyPath(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)
	client := h.apiKeyClient()

	create := connect.NewRequest(&apikeyv1.CreateRequest{
		ProjectId: proj.ID.String(),
		Name:      "ci",
		Scopes:    []string{authn.ScopeSheetsRead},
	})
	withBearer(create.Header())
	createResp, err := client.Create(t.Context(), create)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	revoke := connect.NewRequest(&apikeyv1.RevokeRequest{
		ProjectId: proj.ID.String(),
		ApiKeyId:  createResp.Msg.GetApiKey().GetId(),
	})
	withBearer(revoke.Header())
	revokeResp, err := client.Revoke(t.Context(), revoke)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if revokeResp.Msg.GetApiKey().GetRevokedAt() == nil {
		t.Error("revoked_at is unset after Revoke")
	}
}

func TestAPIKey_List_CrossOrgProject_ReturnsPermissionDenied(t *testing.T) {
	h := newHarness(t, humanPrincipal(uuid.New()))
	proj := h.seedProject(uuid.New())

	req := connect.NewRequest(&apikeyv1.ListRequest{ProjectId: proj.ID.String()})
	withBearer(req.Header())
	if _, err := h.apiKeyClient().List(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodePermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", got)
	}
}

func TestAPIKey_List_MissingPrincipal_ReturnsUnauthenticated(t *testing.T) {
	h := newHarness(t, session.Principal{})

	req := connect.NewRequest(&apikeyv1.ListRequest{ProjectId: uuid.New().String()})
	if _, err := h.apiKeyClient().List(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

func TestAPIKey_Create_APIKeyPrincipal_Succeeds(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, keyPrincipal(orgID))
	proj := h.seedProject(orgID)

	req := connect.NewRequest(&apikeyv1.CreateRequest{
		ProjectId: proj.ID.String(),
		Name:      "minted-by-a-machine",
		Scopes:    []string{authn.ScopeSheetsRead},
	})
	withBearer(req.Header())
	resp, err := h.apiKeyClient().Create(t.Context(), req)
	if err != nil {
		t.Fatalf("Create with an api-key principal: %v", err)
	}
	if resp.Msg.GetPlaintextKey() == "" {
		t.Error("Create returned no plaintext key")
	}
}

func TestAPIKey_Create_UnknownScope_ReturnsInvalidArgument(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)

	req := connect.NewRequest(&apikeyv1.CreateRequest{
		ProjectId: proj.ID.String(),
		Name:      "ci",
		Scopes:    []string{"sheets:everything"},
	})
	withBearer(req.Header())
	if _, err := h.apiKeyClient().Create(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got)
	}
}

func TestAPIKey_Revoke_UnknownID_ReturnsNotFound(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)

	req := connect.NewRequest(&apikeyv1.RevokeRequest{ProjectId: proj.ID.String(), ApiKeyId: uuid.New().String()})
	withBearer(req.Header())
	if _, err := h.apiKeyClient().Revoke(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeNotFound {
		t.Errorf("code = %v, want NotFound", got)
	}
}

func TestAPIKey_Create_WithASheetGrant_EchoesTheSheetIDs(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)
	sh := h.seedSheet(orgID, proj.ID, "prices")
	h.keySheets.Add(orgID, proj.ID, sh.ID)

	req := connect.NewRequest(&apikeyv1.CreateRequest{
		ProjectId: proj.ID.String(),
		Name:      "scoped-to-one-sheet",
		Scopes:    []string{authn.ScopeSheetsRead},
		SheetIds:  []string{sh.ID.String()},
	})
	withBearer(req.Header())
	resp, err := h.apiKeyClient().Create(t.Context(), req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := resp.Msg.GetApiKey().GetSheetIds(); len(got) != 1 || got[0] != sh.ID.String() {
		t.Errorf("sheet_ids = %v, want [%s]", got, sh.ID)
	}
}
