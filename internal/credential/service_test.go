package credential_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

// SECURITY: a syntactically complete service-account key. The private key is a throwaway test fixture.
const serviceAccountJSON = `{
  "type": "service_account",
  "project_id": "opensheet-test",
  "private_key_id": "abc123",
  "private_key": "-----BEGIN PRIVATE KEY-----\nc2VjcmV0LXRlc3Qta2V5LW1hdGVyaWFs\n-----END PRIVATE KEY-----\n",
  "client_email": "reader@opensheet-test.iam.gserviceaccount.com",
  "client_id": "1234567890",
  "token_uri": "https://oauth2.googleapis.com/token"
}`

// SECURITY: an external_account config pointing at an attacker-controlled token URL. Must be rejected.
const externalAccountJSON = `{
  "type": "external_account",
  "audience": "//iam.googleapis.com/projects/1/locations/global/workloadIdentityPools/p/providers/v",
  "subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
  "token_url": "https://attacker.example.com/token",
  "credential_source": {"url": "https://attacker.example.com/subject-token"}
}`

func testSealer(t *testing.T) sealer.Sealer {
	t.Helper()
	key, err := hex.DecodeString(strings.Repeat("ab", sealer.KeyLen))
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	s, err := sealer.New(key)
	if err != nil {
		t.Fatalf("sealer.New: %v", err)
	}
	return s
}

func newSvc(t *testing.T, store credential.Store, sl sealer.Sealer) (*credential.Service, *int) {
	t.Helper()
	calls := 0
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		calls++
		return apperror.New("opensheet.unexpected", err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return credential.NewService(store, log, unexpected, sl), &calls
}

func tenantCtx(t *testing.T) (context.Context, tenant.Context) {
	t.Helper()
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
	return tenant.Into(context.Background(), tc), tc
}

func upload(ctx context.Context, t *testing.T, svc *credential.Service, name string) *credential.Credential {
	t.Helper()
	c, err := svc.UploadServiceAccount(ctx, name, []byte(serviceAccountJSON))
	if err != nil {
		t.Fatalf("UploadServiceAccount: %v", err)
	}
	return c
}

func TestService_UploadServiceAccount(t *testing.T) {
	t.Run("seals the payload and stores ciphertext only", func(t *testing.T) {
		sl := testSealer(t)
		svc, unex := newSvc(t, fakes.NewCredential(), sl)
		ctx, tc := tenantCtx(t)

		c := upload(ctx, t, svc, "  prod reader  ")
		if c.Name != "prod reader" {
			t.Errorf("Name=%q want trimmed", c.Name)
		}
		if c.Kind != credential.KindServiceAccount {
			t.Errorf("Kind=%q", c.Kind)
		}
		if c.Status != credential.StatusActive {
			t.Errorf("Status=%q", c.Status)
		}
		if c.GoogleAccountEmail != "reader@opensheet-test.iam.gserviceaccount.com" {
			t.Errorf("GoogleAccountEmail=%q", c.GoogleAccountEmail)
		}
		if c.OrgID != tc.OrgID || c.ProjectID != tc.ProjectID || c.AuthorizedByUserID != tc.UserID {
			t.Errorf("scope not carried: %+v", c)
		}
		if strings.Contains(string(c.Sealed), "private_key") {
			t.Fatal("Sealed carries plaintext")
		}
		opened, err := sl.Open(c.Sealed, credential.SealAAD(c.OrgID, c.ProjectID, c.ID))
		if err != nil {
			t.Fatalf("the ciphertext must open under the row's own AAD: %v", err)
		}
		if string(opened) != serviceAccountJSON {
			t.Error("round trip lost the payload")
		}
		if *unex != 0 {
			t.Errorf("unexpected() called %d times", *unex)
		}
	})

	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
		_, err := svc.UploadServiceAccount(context.Background(), "prod", []byte(serviceAccountJSON))
		if !tenant.IsMissingError(err) {
			t.Fatalf("error = %T %v, want tenant.MissingError", err, err)
		}
	})

	// SECURITY: an inferred credential type would accept an external_account config pointing at an
	// attacker-controlled token URL.
	t.Run("rejects an external_account payload", func(t *testing.T) {
		svc, unex := newSvc(t, fakes.NewCredential(), testSealer(t))
		ctx, _ := tenantCtx(t)
		_, err := svc.UploadServiceAccount(ctx, "sneaky", []byte(externalAccountJSON))
		if !credential.IsInvalidKindError(err) {
			t.Fatalf("error = %T %v, want *InvalidKindError", err, err)
		}
		if strings.Contains(err.Error(), "attacker.example.com") {
			t.Errorf("the error echoes the attacker-controlled token URL: %q", err)
		}
		if *unex != 0 {
			t.Error("a rejected payload must not route through unexpected")
		}
	})

	t.Run("rejects other google credential types", func(t *testing.T) {
		for _, kind := range []string{"authorized_user", "impersonated_service_account", "gdch_service_account", ""} {
			svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
			ctx, _ := tenantCtx(t)
			payload := []byte(`{"type":"` + kind + `","client_email":"x@y.com"}`)
			if _, err := svc.UploadServiceAccount(ctx, "n", payload); !credential.IsInvalidKindError(err) {
				t.Errorf("type=%q error = %T %v, want *InvalidKindError", kind, err, err)
			}
		}
	})

	t.Run("rejects malformed and incomplete payloads", func(t *testing.T) {
		tests := map[string]string{
			"not json":            `not json at all`,
			"missing type":        `{"client_email":"x@y.com"}`,
			"missing email":       `{"type":"service_account","private_key":"k"}`,
			"blank email":         `{"type":"service_account","client_email":"   "}`,
			"missing private key": `{"type":"service_account","client_email":"x@y.com"}`,
			"field google cannot decode": `{"type":"service_account","client_email":"x@y.com",` +
				`"private_key":"k","universe_domain":42}`,
			"absurdly long type":    `{"type":"` + strings.Repeat("a", 41) + `","client_email":"x@y.com"}`,
			"type with punctuation": `{"type":"service-account!","client_email":"x@y.com"}`,
		}
		for name, payload := range tests {
			t.Run(name, func(t *testing.T) {
				svc, unex := newSvc(t, fakes.NewCredential(), testSealer(t))
				ctx, _ := tenantCtx(t)
				_, err := svc.UploadServiceAccount(ctx, "n", []byte(payload))
				if err == nil {
					t.Fatal("want an error")
				}
				if !credential.IsInvalidServiceAccountError(err) && !credential.IsInvalidKindError(err) {
					t.Fatalf("error = %T %v, want a typed rejection", err, err)
				}
				if *unex != 0 {
					t.Error("a rejected payload must not route through unexpected")
				}
			})
		}
	})

	// SECURITY: a token_uri the uploader controls would turn an upload into a request to their host.
	t.Run("rejects a token_uri that is not google's", func(t *testing.T) {
		for _, uri := range []string{
			"https://attacker.example.com/token",
			"http://oauth2.googleapis.com/token",
			"https://oauth2.googleapis.com.attacker.example.com/token",
			"://nonsense",
		} {
			svc, unex := newSvc(t, fakes.NewCredential(), testSealer(t))
			ctx, _ := tenantCtx(t)
			payload := []byte(`{"type":"service_account","client_email":"x@y.com","private_key":"k","token_uri":"` + uri + `"}`)
			_, err := svc.UploadServiceAccount(ctx, "n", payload)
			if !credential.IsInvalidServiceAccountError(err) {
				t.Errorf("token_uri=%q error = %T %v, want *InvalidServiceAccountError", uri, err, err)
			}
			if *unex != 0 {
				t.Error("a rejected payload must not route through unexpected")
			}
		}
	})

	t.Run("accepts a key with no token_uri", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
		ctx, _ := tenantCtx(t)
		payload := []byte(`{"type":"service_account","client_email":"x@y.com","private_key":"k"}`)
		if _, err := svc.UploadServiceAccount(ctx, "n", payload); err != nil {
			t.Fatalf("UploadServiceAccount: %v", err)
		}
	})

	t.Run("invalid name bubbles the typed error", func(t *testing.T) {
		svc, unex := newSvc(t, fakes.NewCredential(), testSealer(t))
		ctx, _ := tenantCtx(t)
		_, err := svc.UploadServiceAccount(ctx, "   ", []byte(serviceAccountJSON))
		if !credential.IsInvalidNameError(err) {
			t.Fatalf("error = %T %v, want *InvalidNameError", err, err)
		}
		if *unex != 0 {
			t.Error("an invariant error must not route through unexpected")
		}
	})

	t.Run("a disabled sealer reports encryption unavailable", func(t *testing.T) {
		svc, unex := newSvc(t, fakes.NewCredential(), sealer.Disabled())
		ctx, _ := tenantCtx(t)
		_, err := svc.UploadServiceAccount(ctx, "prod", []byte(serviceAccountJSON))
		if !sealer.IsUnavailableError(err) {
			t.Fatalf("error = %T %v, want sealer.UnavailableError", err, err)
		}
		if *unex != 0 {
			t.Error("a missing key is a configuration failure, not an unexpected one")
		}
	})

	t.Run("a duplicate name bubbles AlreadyExistsError", func(t *testing.T) {
		svc, unex := newSvc(t, fakes.NewCredential(), testSealer(t))
		ctx, _ := tenantCtx(t)
		upload(ctx, t, svc, "prod")
		_, err := svc.UploadServiceAccount(ctx, "prod", []byte(serviceAccountJSON))
		if !credential.IsAlreadyExistsError(err) {
			t.Fatalf("error = %T %v, want *AlreadyExistsError", err, err)
		}
		if *unex != 0 {
			t.Error("a name collision must not route through unexpected")
		}
	})

	t.Run("a store failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewCredential()
		store.SaveFn = func(context.Context, *credential.Credential) error { return errors.New("boom") }
		svc, unex := newSvc(t, store, testSealer(t))
		ctx, _ := tenantCtx(t)
		if _, err := svc.UploadServiceAccount(ctx, "prod", []byte(serviceAccountJSON)); err == nil {
			t.Fatal("want an error")
		}
		if *unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *unex)
		}
	})
}

// SECURITY: the uploaded key must not reach an error string on any failure path.
func TestService_ErrorsNeverCarryTheUploadedPayload(t *testing.T) {
	secrets := []string{
		"c2VjcmV0LXRlc3Qta2V5LW1hdGVyaWFs",
		"BEGIN PRIVATE KEY",
		"abc123",
		"1234567890",
	}
	payloads := map[string][]byte{
		"valid":            []byte(serviceAccountJSON),
		"external account": []byte(externalAccountJSON),
		"broken json":      []byte(serviceAccountJSON + "trailing garbage"),
	}
	sealers := map[string]sealer.Sealer{"working": testSealer(t), "disabled": sealer.Disabled()}
	stores := map[string]func() credential.Store{
		"in memory": func() credential.Store { return fakes.NewCredential() },
		"failing": func() credential.Store {
			s := fakes.NewCredential()
			s.SaveFn = func(_ context.Context, c *credential.Credential) error {
				return errors.New("save failed for " + c.Name)
			}
			return s
		},
	}

	for pName, payload := range payloads {
		for sName, sl := range sealers {
			for stName, mk := range stores {
				t.Run(pName+"/"+sName+"/"+stName, func(t *testing.T) {
					svc, _ := newSvc(t, mk(), sl)
					ctx, _ := tenantCtx(t)
					_, err := svc.UploadServiceAccount(ctx, "prod", payload)
					if err == nil {
						return
					}
					msg := err.Error()
					if strings.Contains(msg, string(payload)) {
						t.Fatalf("error string carries the whole uploaded payload: %q", msg)
					}
					for _, secret := range secrets {
						if strings.Contains(msg, secret) {
							t.Errorf("error string leaks %q: %q", secret, msg)
						}
					}
				})
			}
		}
	}
}

func TestService_TokenSourceFor(t *testing.T) {
	t.Run("returns a token source without exposing plaintext", func(t *testing.T) {
		svc, unex := newSvc(t, fakes.NewCredential(), testSealer(t))
		ctx, _ := tenantCtx(t)
		c := upload(ctx, t, svc, "prod")

		ts, err := svc.TokenSourceFor(ctx, c.ID)
		if err != nil {
			t.Fatalf("TokenSourceFor: %v", err)
		}
		if ts == nil {
			t.Fatal("TokenSourceFor returned a nil token source")
		}
		if *unex != 0 {
			t.Errorf("unexpected() called %d times", *unex)
		}
	})

	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
		if _, err := svc.TokenSourceFor(context.Background(), uuid.New()); !tenant.IsMissingError(err) {
			t.Fatalf("error = %T %v, want tenant.MissingError", err, err)
		}
	})

	t.Run("unknown id is NotFoundError", func(t *testing.T) {
		svc, unex := newSvc(t, fakes.NewCredential(), testSealer(t))
		ctx, _ := tenantCtx(t)
		if _, err := svc.TokenSourceFor(ctx, uuid.New()); !credential.IsNotFoundError(err) {
			t.Fatalf("error = %T %v, want *NotFoundError", err, err)
		}
		if *unex != 0 {
			t.Error("a miss must not route through unexpected")
		}
	})

	t.Run("another tenant's credential is NotFoundError", func(t *testing.T) {
		store := fakes.NewCredential()
		svc, _ := newSvc(t, store, testSealer(t))
		ctxA, _ := tenantCtx(t)
		c := upload(ctxA, t, svc, "prod")

		ctxB, _ := tenantCtx(t)
		if _, err := svc.TokenSourceFor(ctxB, c.ID); !credential.IsNotFoundError(err) {
			t.Fatalf("error = %T %v, want *NotFoundError", err, err)
		}
	})

	t.Run("a credential needing reauth refuses", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
		ctx, _ := tenantCtx(t)
		c := upload(ctx, t, svc, "prod")
		if _, err := svc.MarkReauthNeeded(ctx, c.ID); err != nil {
			t.Fatalf("MarkReauthNeeded: %v", err)
		}
		if _, err := svc.TokenSourceFor(ctx, c.ID); !credential.IsReauthNeededError(err) {
			t.Fatalf("error = %T %v, want *ReauthNeededError", err, err)
		}
	})

	t.Run("a google_oauth credential is not yet supported", func(t *testing.T) {
		store := fakes.NewCredential()
		svc, _ := newSvc(t, store, testSealer(t))
		ctx, tc := tenantCtx(t)
		c, err := credential.New(uuid.Nil, tc.OrgID, tc.ProjectID, tc.UserID,
			"oauth", credential.KindGoogleOAuth, "u@x.com", []byte("sealed"))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		store.Seed(c)
		if _, tsErr := svc.TokenSourceFor(ctx, c.ID); !credential.IsInvalidKindError(tsErr) {
			t.Fatalf("error = %T %v, want *InvalidKindError", tsErr, tsErr)
		}
	})

	// SECURITY: the AAD binds a ciphertext to one row and one tenant, so a stolen row cannot be replayed.
	t.Run("a ciphertext sealed for another credential fails to open", func(t *testing.T) {
		sl := testSealer(t)
		store := fakes.NewCredential()
		svc, _ := newSvc(t, store, sl)
		ctx, _ := tenantCtx(t)

		victim := upload(ctx, t, svc, "victim")
		thief := upload(ctx, t, svc, "thief")

		thief.Sealed = victim.Sealed
		store.Seed(thief)

		_, err := svc.TokenSourceFor(ctx, thief.ID)
		if !sealer.IsOpenFailedError(err) {
			t.Fatalf("error = %T %v, want sealer.OpenFailedError", err, err)
		}
		if sealer.IsUnavailableError(err) {
			t.Error("a tampered row must not read as a missing key")
		}
	})

	t.Run("a rotated key reports open failed, not unavailable", func(t *testing.T) {
		store := fakes.NewCredential()
		svc, _ := newSvc(t, store, testSealer(t))
		ctx, _ := tenantCtx(t)
		c := upload(ctx, t, svc, "prod")

		otherKey := make([]byte, sealer.KeyLen)
		for i := range otherKey {
			otherKey[i] = 0x11
		}
		rotated, err := sealer.New(otherKey)
		if err != nil {
			t.Fatalf("sealer.New: %v", err)
		}
		svcAfterRotation, unex := newSvc(t, store, rotated)
		_, err = svcAfterRotation.TokenSourceFor(ctx, c.ID)
		if !sealer.IsOpenFailedError(err) {
			t.Fatalf("error = %T %v, want sealer.OpenFailedError", err, err)
		}
		if *unex != 0 {
			t.Error("an undecryptable row is a key problem, not an unexpected one")
		}
	})

	t.Run("a disabled sealer reports encryption unavailable", func(t *testing.T) {
		store := fakes.NewCredential()
		svc, _ := newSvc(t, store, testSealer(t))
		ctx, _ := tenantCtx(t)
		c := upload(ctx, t, svc, "prod")

		disabled, _ := newSvc(t, store, sealer.Disabled())
		_, err := disabled.TokenSourceFor(ctx, c.ID)
		if !sealer.IsUnavailableError(err) {
			t.Fatalf("error = %T %v, want sealer.UnavailableError", err, err)
		}
		if sealer.IsOpenFailedError(err) {
			t.Error("a missing key must not read as a tampered row")
		}
	})

	t.Run("a row whose plaintext is not a service account key is rejected", func(t *testing.T) {
		sl := testSealer(t)
		store := fakes.NewCredential()
		svc, _ := newSvc(t, store, sl)
		ctx, tc := tenantCtx(t)

		id := uuid.Must(uuid.NewV7())
		sealed, err := sl.Seal([]byte(externalAccountJSON), credential.SealAAD(tc.OrgID, tc.ProjectID, id))
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		c, err := credential.New(id, tc.OrgID, tc.ProjectID, tc.UserID,
			"tampered", credential.KindServiceAccount, "x@y.com", sealed)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		store.Seed(c)

		_, err = svc.TokenSourceFor(ctx, c.ID)
		if !credential.IsInvalidServiceAccountError(err) {
			t.Fatalf("error = %T %v, want *InvalidServiceAccountError", err, err)
		}
		if strings.Contains(err.Error(), "attacker.example.com") {
			t.Errorf("the error echoes the stored payload: %q", err)
		}
	})

	t.Run("a row carrying no ciphertext is rejected", func(t *testing.T) {
		store := fakes.NewCredential()
		svc, _ := newSvc(t, store, testSealer(t))
		ctx, tc := tenantCtx(t)

		c, err := credential.New(uuid.Nil, tc.OrgID, tc.ProjectID, tc.UserID,
			"empty", credential.KindServiceAccount, "x@y.com", []byte("x"))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		c.Sealed = nil
		store.Seed(c)

		if _, tsErr := svc.TokenSourceFor(ctx, c.ID); !credential.IsNotSealedError(tsErr) {
			t.Fatalf("error = %T %v, want *NotSealedError", tsErr, tsErr)
		}
	})

	t.Run("a store failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewCredential()
		store.ByIDFn = func(context.Context, uuid.UUID) (*credential.Credential, error) {
			return nil, errors.New("boom")
		}
		svc, unex := newSvc(t, store, testSealer(t))
		ctx, _ := tenantCtx(t)
		if _, err := svc.TokenSourceFor(ctx, uuid.New()); err == nil {
			t.Fatal("want an error")
		}
		if *unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *unex)
		}
	})
}

// SECURITY: TokenSourceFor hands back a token source, never the decrypted key.
func TestService_TokenSourceFor_ReturnsNoPlaintext(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
	ctx, _ := tenantCtx(t)
	c := upload(ctx, t, svc, "prod")

	ts, err := svc.TokenSourceFor(ctx, c.ID)
	if err != nil {
		t.Fatalf("TokenSourceFor: %v", err)
	}
	rendered, err := json.Marshal(struct{ TS any }{TS: ts})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(rendered), "PRIVATE KEY") {
		t.Fatalf("the returned token source exposes the private key: %s", rendered)
	}
}

func TestService_ByID(t *testing.T) {
	t.Run("returns a credential in scope", func(t *testing.T) {
		svc, unex := newSvc(t, fakes.NewCredential(), testSealer(t))
		ctx, _ := tenantCtx(t)
		c := upload(ctx, t, svc, "prod")

		got, err := svc.ByID(ctx, c.ID)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if got.ID != c.ID || got.Name != "prod" {
			t.Errorf("got %+v", got)
		}
		if *unex != 0 {
			t.Errorf("unexpected() called %d times", *unex)
		}
	})

	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
		if _, err := svc.ByID(context.Background(), uuid.New()); !tenant.IsMissingError(err) {
			t.Fatalf("error = %T %v, want tenant.MissingError", err, err)
		}
	})

	t.Run("unknown id is NotFoundError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
		ctx, _ := tenantCtx(t)
		if _, err := svc.ByID(ctx, uuid.New()); !credential.IsNotFoundError(err) {
			t.Fatalf("error = %T %v, want *NotFoundError", err, err)
		}
	})

	t.Run("another project's credential is NotFoundError", func(t *testing.T) {
		store := fakes.NewCredential()
		svc, _ := newSvc(t, store, testSealer(t))
		ctx, tc := tenantCtx(t)
		c := upload(ctx, t, svc, "prod")

		other := tenant.Into(context.Background(),
			tenant.Context{OrgID: tc.OrgID, ProjectID: uuid.New(), UserID: tc.UserID})
		if _, err := svc.ByID(other, c.ID); !credential.IsNotFoundError(err) {
			t.Fatalf("error = %T %v, want *NotFoundError", err, err)
		}
	})

	t.Run("a store failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewCredential()
		store.ByIDFn = func(context.Context, uuid.UUID) (*credential.Credential, error) {
			return nil, errors.New("boom")
		}
		svc, unex := newSvc(t, store, testSealer(t))
		ctx, _ := tenantCtx(t)
		if _, err := svc.ByID(ctx, uuid.New()); err == nil {
			t.Fatal("want an error")
		}
		if *unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *unex)
		}
	})
}

func TestService_List(t *testing.T) {
	t.Run("returns only the caller's project", func(t *testing.T) {
		store := fakes.NewCredential()
		svc, unex := newSvc(t, store, testSealer(t))
		ctxA, _ := tenantCtx(t)
		upload(ctxA, t, svc, "a")
		upload(ctxA, t, svc, "b")

		ctxB, _ := tenantCtx(t)
		upload(ctxB, t, svc, "elsewhere")

		got, err := svc.List(ctxA)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d credentials, want 2", len(got))
		}
		if *unex != 0 {
			t.Errorf("unexpected() called %d times", *unex)
		}
	})

	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
		if _, err := svc.List(context.Background()); !tenant.IsMissingError(err) {
			t.Fatalf("error = %T %v, want tenant.MissingError", err, err)
		}
	})

	t.Run("a store failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewCredential()
		store.ListFn = func(context.Context, uuid.UUID, uuid.UUID) ([]*credential.Credential, error) {
			return nil, errors.New("boom")
		}
		svc, unex := newSvc(t, store, testSealer(t))
		ctx, _ := tenantCtx(t)
		if _, err := svc.List(ctx); err == nil {
			t.Fatal("want an error")
		}
		if *unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *unex)
		}
	})
}

func TestService_Delete(t *testing.T) {
	t.Run("removes a credential in scope", func(t *testing.T) {
		svc, unex := newSvc(t, fakes.NewCredential(), testSealer(t))
		ctx, _ := tenantCtx(t)
		c := upload(ctx, t, svc, "prod")

		if err := svc.Delete(ctx, c.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := svc.ByID(ctx, c.ID); !credential.IsNotFoundError(err) {
			t.Fatalf("after Delete error = %T %v, want *NotFoundError", err, err)
		}
		if *unex != 0 {
			t.Errorf("unexpected() called %d times", *unex)
		}
	})

	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
		if err := svc.Delete(context.Background(), uuid.New()); !tenant.IsMissingError(err) {
			t.Fatalf("error = %T %v, want tenant.MissingError", err, err)
		}
	})

	t.Run("unknown id is NotFoundError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
		ctx, _ := tenantCtx(t)
		if err := svc.Delete(ctx, uuid.New()); !credential.IsNotFoundError(err) {
			t.Fatalf("error = %T %v, want *NotFoundError", err, err)
		}
	})

	t.Run("another tenant's credential is NotFoundError", func(t *testing.T) {
		store := fakes.NewCredential()
		svc, _ := newSvc(t, store, testSealer(t))
		ctxA, _ := tenantCtx(t)
		c := upload(ctxA, t, svc, "prod")

		ctxB, _ := tenantCtx(t)
		if err := svc.Delete(ctxB, c.ID); !credential.IsNotFoundError(err) {
			t.Fatalf("error = %T %v, want *NotFoundError", err, err)
		}
	})

	t.Run("a referenced credential bubbles InUseError", func(t *testing.T) {
		store := fakes.NewCredential()
		svc, unex := newSvc(t, store, testSealer(t))
		ctx, _ := tenantCtx(t)
		c := upload(ctx, t, svc, "prod")

		store.DeleteFn = func(_ context.Context, id uuid.UUID) error {
			return &credential.InUseError{ID: id.String()}
		}
		err := svc.Delete(ctx, c.ID)
		if !credential.IsInUseError(err) {
			t.Fatalf("error = %T %v, want *InUseError", err, err)
		}
		if *unex != 0 {
			t.Error("a RESTRICT violation must not route through unexpected")
		}
	})

	t.Run("a store failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewCredential()
		svc, unex := newSvc(t, store, testSealer(t))
		ctx, _ := tenantCtx(t)
		c := upload(ctx, t, svc, "prod")

		store.DeleteFn = func(context.Context, uuid.UUID) error { return errors.New("boom") }
		if err := svc.Delete(ctx, c.ID); err == nil {
			t.Fatal("want an error")
		}
		if *unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *unex)
		}
	})
}

func TestService_MarkReauthNeeded(t *testing.T) {
	t.Run("flips status and persists", func(t *testing.T) {
		svc, unex := newSvc(t, fakes.NewCredential(), testSealer(t))
		ctx, _ := tenantCtx(t)
		c := upload(ctx, t, svc, "prod")

		got, err := svc.MarkReauthNeeded(ctx, c.ID)
		if err != nil {
			t.Fatalf("MarkReauthNeeded: %v", err)
		}
		if got.Status != credential.StatusReauthNeeded {
			t.Errorf("Status=%q", got.Status)
		}
		reloaded, err := svc.ByID(ctx, c.ID)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if reloaded.Status != credential.StatusReauthNeeded {
			t.Error("the new status was not persisted")
		}
		if *unex != 0 {
			t.Errorf("unexpected() called %d times", *unex)
		}
	})

	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
		if _, err := svc.MarkReauthNeeded(context.Background(), uuid.New()); !tenant.IsMissingError(err) {
			t.Fatalf("error = %T %v, want tenant.MissingError", err, err)
		}
	})

	t.Run("unknown id is NotFoundError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
		ctx, _ := tenantCtx(t)
		if _, err := svc.MarkReauthNeeded(ctx, uuid.New()); !credential.IsNotFoundError(err) {
			t.Fatalf("error = %T %v, want *NotFoundError", err, err)
		}
	})

	t.Run("a store failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewCredential()
		svc, unex := newSvc(t, store, testSealer(t))
		ctx, _ := tenantCtx(t)
		c := upload(ctx, t, svc, "prod")

		store.SaveFn = func(context.Context, *credential.Credential) error { return errors.New("boom") }
		if _, err := svc.MarkReauthNeeded(ctx, c.ID); err == nil {
			t.Fatal("want an error")
		}
		if *unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *unex)
		}
	})
}
