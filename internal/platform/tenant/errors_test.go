package tenant_test

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform/tenant"
)

func TestMissingError_Error(t *testing.T) {
	me := &tenant.MissingError{}
	if got := me.Error(); got != "tenant: missing context" {
		t.Errorf("Error()=%q", got)
	}
}

func TestIsMissingError_UnwrapsThroughFmtErrorf(t *testing.T) {
	wrapped := fmt.Errorf("service: %w", &tenant.MissingError{})
	if !tenant.IsMissingError(wrapped) {
		t.Fatal("IsMissingError should walk fmt.Errorf %w chains")
	}
}

func TestIsMissingError_False_OnPlainError(t *testing.T) {
	if tenant.IsMissingError(errors.New("nope")) {
		t.Fatal("plain error must not match")
	}
	if tenant.IsMissingError(nil) {
		t.Fatal("nil error must not match")
	}
}

func TestMissingError_ToAppError_ProducesUnauthenticatedEnvelope(t *testing.T) {
	me := &tenant.MissingError{}
	ae := me.ToAppError()
	if ae == nil {
		t.Fatal("ToAppError returned nil")
	}
	if ae.Code() != apperror.CodeTenantMissing {
		t.Errorf("Code()=%q want %q", ae.Code(), apperror.CodeTenantMissing)
	}
	if ae.GRPCCode() != codes.Unauthenticated {
		t.Errorf("GRPCCode()=%v want Unauthenticated", ae.GRPCCode())
	}
	if len(ae.Details()) != 1 {
		t.Errorf("Details() len=%d want 1", len(ae.Details()))
	}
}

func TestAsAppError_ThroughMissingError(t *testing.T) {
	wrapped := fmt.Errorf("layer: %w", &tenant.MissingError{})
	ae, ok := apperror.AsAppError(wrapped)
	if !ok {
		t.Fatal("AsAppError should discover MissingError via ToAppError")
	}
	if ae.Code() != apperror.CodeTenantMissing {
		t.Errorf("Code()=%q want %q", ae.Code(), apperror.CodeTenantMissing)
	}
}
