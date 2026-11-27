package credential_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func deniedFor(t *testing.T, err error, want authz.Permission) {
	t.Helper()
	d, ok := errors.AsType[*authz.DeniedError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want *authz.DeniedError", err, err)
	}
	if d.Permission != want {
		t.Fatalf("denied %s, want %s", d.Permission, want)
	}
}

func asRole(tc tenant.Context, userID uuid.UUID, role string) context.Context {
	tc.UserID = userID
	return fakes.BindRole(context.Background(), tc, role)
}

func keyOf(tc tenant.Context, creator uuid.UUID, role string) context.Context {
	ctx := fakes.BindRole(context.Background(), tc, role)
	p := session.PrincipalFrom(ctx)
	p.UserID, p.KeyID, p.KeyOwnerID, p.Source = uuid.Nil, uuid.New(), creator, session.SourceAPIKey
	return session.PrincipalInto(ctx, p)
}

func TestGatedMethods_RefuseEmptyHeldSet(t *testing.T) {
	store := fakes.NewCredential()
	svc, _ := newSvc(t, store, testSealer(t))
	ctx, tc := tenantCtx(t)
	c := upload(ctx, t, svc, "prod")
	empty := fakes.BindRole(context.Background(), tc, "")
	f := newConnect(t, tokenResponse{})
	cases := []struct {
		name string
		want authz.Permission
		call func(context.Context) error
	}{
		{"UploadServiceAccount", authz.CredentialsWrite, func(ctx context.Context) error {
			_, err := svc.UploadServiceAccount(ctx, "other", []byte(serviceAccountJSON))
			return err
		}},
		{"ConnectStart", authz.CredentialsWrite, func(ctx context.Context) error {
			_, err := f.workflow.Start(ctx, tc.OrgID, tc.ProjectID, tc.UserID, testReturnTo)
			return err
		}},
		{"Delete", authz.CredentialsWrite, func(ctx context.Context) error { return svc.Delete(ctx, c.ID) }},
		{"List", authz.CredentialsRead, func(ctx context.Context) error {
			_, err := svc.List(ctx)
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { deniedFor(t, c.call(empty), c.want) })
	}
}

func TestCredentialGate_RunsBeforeValidation(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
	_, tc := tenantCtx(t)
	_, err := svc.UploadServiceAccount(fakes.BindRole(context.Background(), tc, ""), "x", []byte("not json"))
	deniedFor(t, err, authz.CredentialsWrite)
}

func TestConnectStart_HeldSetOfAnotherOrgRefused(t *testing.T) {
	f := newConnect(t, tokenResponse{})
	_, tc := tenantCtx(t)
	_, err := f.workflow.Start(fakes.BindRole(context.Background(), tc, "owner"), uuid.New(), tc.ProjectID, tc.UserID, testReturnTo)
	deniedFor(t, err, authz.CredentialsWrite)
}

func TestCredential_MemberDeletesOwn(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
	_, tc := tenantCtx(t)
	member := asRole(tc, tc.UserID, "member")
	c := upload(member, t, svc, "mine")

	if err := svc.Delete(member, c.ID); err != nil {
		t.Fatalf("member deletes own: %v", err)
	}
}

func TestCredential_MemberDeleteOthersIsROL001(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
	ctx, tc := tenantCtx(t)
	c := upload(ctx, t, svc, "theirs")

	err := svc.Delete(asRole(tc, uuid.New(), "member"), c.ID)
	deniedFor(t, err, authz.CredentialsManage)
	if d, _ := errors.AsType[*authz.DeniedError](err); d.ToAppError().Code() != "ROL001" {
		t.Errorf("code = %s, want ROL001", d.ToAppError().Code())
	}
}

func TestCredential_AdminDeletesAny(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
	ctx, tc := tenantCtx(t)
	c := upload(ctx, t, svc, "theirs")

	if err := svc.Delete(asRole(tc, uuid.New(), "admin"), c.ID); err != nil {
		t.Fatalf("admin deletes any: %v", err)
	}
}

func TestCredential_KeyDeleteUsesCreator(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
	_, tc := tenantCtx(t)
	c := upload(asRole(tc, tc.UserID, "member"), t, svc, "mine")
	other := upload(asRole(tc, uuid.New(), "member"), t, svc, "theirs")

	err := svc.Delete(keyOf(tc, uuid.New(), "member"), c.ID)
	deniedFor(t, err, authz.CredentialsManage)
	if err := svc.Delete(keyOf(tc, tc.UserID, "member"), c.ID); err != nil {
		t.Fatalf("creator's key deletes the creator's credential: %v", err)
	}
	deniedFor(t, svc.Delete(keyOf(tc, tc.UserID, "member"), other.ID), authz.CredentialsManage)
}

func TestCredentialLookups_WorkBeforeBind(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewCredential(), testSealer(t))
	ctx, tc := tenantCtx(t)
	c := upload(ctx, t, svc, "prod")
	unbound := tenant.Into(context.Background(), tc)

	if _, err := svc.ByID(unbound, c.ID); err != nil {
		t.Errorf("ByID: %v", err)
	}
	if _, err := svc.MarkReauthNeeded(unbound, c.ID); err != nil && !credential.IsNotFoundError(err) {
		if _, ok := errors.AsType[*authz.DeniedError](err); ok {
			t.Errorf("MarkReauthNeeded gated: %v", err)
		}
	}
}
