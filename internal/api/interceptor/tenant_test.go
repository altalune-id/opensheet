package interceptor_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"altalune.id/template/internal/api/interceptor"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/platform/tenant"
)

func TestTenant_ExtractsFromPrincipal(t *testing.T) {
	inter := interceptor.Tenant()
	userID := uuid.New()
	orgID := uuid.New()
	projID := uuid.New()
	p := session.Principal{UserID: userID, ActiveOrgID: orgID, ActiveProjectID: projID}

	var got tenant.Context
	next := connect.UnaryFunc(func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		tc, err := tenant.From(ctx)
		if err != nil {
			t.Fatalf("tenant.From: %v", err)
		}
		got = tc
		return connect.NewResponse(&struct{}{}), nil
	})
	ctx := session.PrincipalInto(context.Background(), p)
	if _, err := inter(next)(ctx, newReq()); err != nil {
		t.Fatal(err)
	}
	if got.OrgID != orgID || got.ProjectID != projID || got.UserID != userID {
		t.Errorf("tenant mismatch: %+v", got)
	}
}

func TestTenant_NoPrincipal_LeavesCtxAlone(t *testing.T) {
	inter := interceptor.Tenant()
	next := connect.UnaryFunc(func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		if _, err := tenant.From(ctx); err == nil {
			t.Error("tenant.Context should be absent when principal is empty")
		}
		return connect.NewResponse(&struct{}{}), nil
	})
	if _, err := inter(next)(context.Background(), newReq()); err != nil {
		t.Fatal(err)
	}
}
