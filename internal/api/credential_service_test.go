package api_test

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	credentialv1 "altalune.id/opensheet/gen/go/credential/v1"
	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/platform/session"
)

const serviceAccountJSON = `{
  "type": "service_account",
  "project_id": "opensheet-test",
  "private_key_id": "abc123",
  "private_key": "-----BEGIN PRIVATE KEY-----\nc2VjcmV0LXRlc3Qta2V5LW1hdGVyaWFs\n-----END PRIVATE KEY-----\n",
  "client_email": "reader@opensheet-test.iam.gserviceaccount.com",
  "client_id": "1234567890",
  "token_uri": "https://oauth2.googleapis.com/token"
}`

func TestCredential_UploadAndList_HappyPath(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)
	client := h.credentialClient()

	up := connect.NewRequest(&credentialv1.UploadServiceAccountRequest{
		ProjectId:          proj.ID.String(),
		Name:               "prod",
		ServiceAccountJson: []byte(serviceAccountJSON),
	})
	withBearer(up.Header())
	upResp, err := client.UploadServiceAccount(t.Context(), up)
	if err != nil {
		t.Fatalf("UploadServiceAccount: %v", err)
	}
	got := upResp.Msg.GetCredential()
	if got.GetName() != "prod" {
		t.Errorf("name = %q, want prod", got.GetName())
	}
	if got.GetKind() != string(credential.KindServiceAccount) {
		t.Errorf("kind = %q, want service_account", got.GetKind())
	}
	if got.GetGoogleAccountEmail() != "reader@opensheet-test.iam.gserviceaccount.com" {
		t.Errorf("google_account_email = %q", got.GetGoogleAccountEmail())
	}
	if got.GetProjectId() != proj.ID.String() {
		t.Errorf("project_id = %q, want %q", got.GetProjectId(), proj.ID.String())
	}

	list := connect.NewRequest(&credentialv1.ListRequest{ProjectId: proj.ID.String()})
	withBearer(list.Header())
	listResp, err := client.List(t.Context(), list)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if n := len(listResp.Msg.GetCredentials()); n != 1 {
		t.Fatalf("credentials len = %d, want 1", n)
	}
}

// SECURITY: the sealed ciphertext and the uploaded plaintext must not appear anywhere in a response message.
func TestCredential_ResponsesCarryNoSealedMaterial(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)
	client := h.credentialClient()

	up := connect.NewRequest(&credentialv1.UploadServiceAccountRequest{
		ProjectId:          proj.ID.String(),
		Name:               "prod",
		ServiceAccountJson: []byte(serviceAccountJSON),
	})
	withBearer(up.Header())
	upResp, err := client.UploadServiceAccount(t.Context(), up)
	if err != nil {
		t.Fatalf("UploadServiceAccount: %v", err)
	}
	list := connect.NewRequest(&credentialv1.ListRequest{ProjectId: proj.ID.String()})
	withBearer(list.Header())
	listResp, err := client.List(t.Context(), list)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	stored, err := h.creds.List(t.Context(), orgID, proj.ID)
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored = %d creds, err %v", len(stored), err)
	}
	if len(stored[0].Sealed) == 0 {
		t.Fatal("nothing was sealed, so the leak assertion would be vacuous")
	}

	for name, msg := range map[string]proto.Message{
		"UploadServiceAccountResponse": upResp.Msg,
		"ListResponse":                 listResp.Msg,
	} {
		wire := prototext.Format(msg)
		for _, needle := range []string{"private_key", "BEGIN PRIVATE KEY", "sealed", string(stored[0].Sealed)} {
			if strings.Contains(wire, needle) {
				t.Errorf("%s leaks %q", name, needle)
			}
		}
	}

	fields := credentialv1.File_credential_v1_credential_proto.Messages().ByName("Credential").Fields()
	for i := range fields.Len() {
		if n := string(fields.Get(i).Name()); n == "sealed" || strings.Contains(n, "secret") {
			t.Errorf("Credential proto declares a %q field", n)
		}
	}
}

func TestCredential_List_CrossOrgProject_ReturnsPermissionDenied(t *testing.T) {
	h := newHarness(t, humanPrincipal(uuid.New()))
	proj := h.seedProject(uuid.New())

	req := connect.NewRequest(&credentialv1.ListRequest{ProjectId: proj.ID.String()})
	withBearer(req.Header())
	if _, err := h.credentialClient().List(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodePermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", got)
	}
}

func TestCredential_List_MissingPrincipal_ReturnsUnauthenticated(t *testing.T) {
	h := newHarness(t, session.Principal{})

	req := connect.NewRequest(&credentialv1.ListRequest{ProjectId: uuid.New().String()})
	if _, err := h.credentialClient().List(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

func TestCredential_List_APIKeyPrincipal_Succeeds(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, keyPrincipal(orgID))
	proj := h.seedProject(orgID)

	req := connect.NewRequest(&credentialv1.ListRequest{ProjectId: proj.ID.String()})
	withBearer(req.Header())
	if _, err := h.credentialClient().List(t.Context(), req); err != nil {
		t.Fatalf("List with an api-key principal: %v", err)
	}
}

func TestCredential_StartGoogleConnect_ReturnsAnAuthorizationURL(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)

	req := connect.NewRequest(&credentialv1.StartGoogleConnectRequest{
		ProjectId: proj.ID.String(),
		ReturnTo:  "/orgs/acme/projects/default/credentials",
	})
	withBearer(req.Header())
	resp, err := h.credentialClient().StartGoogleConnect(t.Context(), req)
	if err != nil {
		t.Fatalf("StartGoogleConnect: %v", err)
	}
	url := resp.Msg.GetAuthorizationUrl()
	if !strings.HasPrefix(url, "https://accounts.google.com/o/oauth2/auth?") {
		t.Fatalf("authorization_url = %q", url)
	}
	if !strings.Contains(url, "state=") {
		t.Errorf("authorization_url carries no state: %q", url)
	}
}

// SECURITY: ConnectWorkflow.Start binds the grant to a user, and a key principal is not a person.
func TestCredential_StartGoogleConnect_RejectsAKeyPrincipal(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, keyPrincipal(orgID))
	proj := h.seedProject(orgID)

	req := connect.NewRequest(&credentialv1.StartGoogleConnectRequest{ProjectId: proj.ID.String()})
	withBearer(req.Header())
	_, err := h.credentialClient().StartGoogleConnect(t.Context(), req)
	if err == nil {
		t.Fatal("a key principal must not be able to start a google connect")
	}
	if got := connectCode(err); got != connect.CodePermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", got)
	}
}

func TestCredential_Delete_HappyPath(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)
	client := h.credentialClient()

	up := connect.NewRequest(&credentialv1.UploadServiceAccountRequest{
		ProjectId:          proj.ID.String(),
		Name:               "prod",
		ServiceAccountJson: []byte(serviceAccountJSON),
	})
	withBearer(up.Header())
	upResp, err := client.UploadServiceAccount(t.Context(), up)
	if err != nil {
		t.Fatalf("UploadServiceAccount: %v", err)
	}

	del := connect.NewRequest(&credentialv1.DeleteRequest{
		ProjectId:    proj.ID.String(),
		CredentialId: upResp.Msg.GetCredential().GetId(),
	})
	withBearer(del.Header())
	if _, err := client.Delete(t.Context(), del); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if left, _ := h.creds.List(t.Context(), orgID, proj.ID); len(left) != 0 {
		t.Errorf("credentials left = %d, want 0", len(left))
	}
}

func TestCredential_Delete_UnknownID_ReturnsNotFound(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)

	req := connect.NewRequest(&credentialv1.DeleteRequest{
		ProjectId:    proj.ID.String(),
		CredentialId: uuid.New().String(),
	})
	withBearer(req.Header())
	if _, err := h.credentialClient().Delete(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeNotFound {
		t.Errorf("code = %v, want NotFound", got)
	}
}

func TestCredential_UploadServiceAccount_RejectsANonServiceAccountPayload(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)

	req := connect.NewRequest(&credentialv1.UploadServiceAccountRequest{
		ProjectId:          proj.ID.String(),
		Name:               "prod",
		ServiceAccountJson: []byte(`{"type":"external_account","token_url":"https://attacker.example.com/token"}`),
	})
	withBearer(req.Header())
	if _, err := h.credentialClient().UploadServiceAccount(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got)
	}
}

func TestCredential_StartGoogleConnect_RejectsAnAbsoluteReturnTo(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)

	req := connect.NewRequest(&credentialv1.StartGoogleConnectRequest{
		ProjectId: proj.ID.String(),
		ReturnTo:  "https://attacker.example.com/steal",
	})
	withBearer(req.Header())
	if _, err := h.credentialClient().StartGoogleConnect(t.Context(), req); err == nil {
		t.Fatal("an off-site return_to must be refused")
	} else if got := connectCode(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got)
	}
}
