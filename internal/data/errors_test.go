package data

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/internal/apperror"
)

func TestNotFoundError(t *testing.T) {
	err := &NotFoundError{}
	if err.Error() == "" {
		t.Error("Error() must not be empty")
	}
	ae := err.ToAppError()
	if ae.Code() != apperror.CodeSheetNotFound {
		t.Errorf("code = %q, want %q", ae.Code(), apperror.CodeSheetNotFound)
	}
	if ae.GRPCCode() != codes.NotFound {
		t.Errorf("grpc code = %v, want NotFound", ae.GRPCCode())
	}
	if ae.HTTPStatus() != http.StatusNotFound {
		t.Errorf("HTTPStatus = %d, want 404", ae.HTTPStatus())
	}
}

func TestIsNotFoundError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"own type", &NotFoundError{}, true},
		{"masked", maskNotFound(apperror.New(apperror.CodeOrgNotFound, "m", codes.NotFound)), true},
		{"foreign", errors.New("other"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNotFoundError(tc.err); got != tc.want {
				t.Errorf("IsNotFoundError = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNotFoundError_CarriesTheMaskedCauseToTheLogOnly(t *testing.T) {
	cause := apperror.New(apperror.CodeProjectNotFound, "Project not found", codes.NotFound)
	masked := maskNotFound(cause)

	if !strings.Contains(masked.Error(), "Project not found") {
		t.Errorf("Error() = %q, want it to name the cause for the log", masked.Error())
	}
	if !errors.Is(masked, cause) {
		t.Error("the masked error must unwrap to its cause")
	}
	nf, ok := errors.AsType[*NotFoundError](masked)
	if !ok {
		t.Fatalf("masked = %T, want *NotFoundError", masked)
	}
	ae := nf.ToAppError()
	if ae.Code() != apperror.CodeSheetNotFound || ae.Message() != "Sheet not found" {
		t.Errorf("wire envelope = %s/%q, want the cause-free SHT001 answer", ae.Code(), ae.Message())
	}
}

func TestMaskNotFound(t *testing.T) {
	cases := []struct {
		name   string
		in     error
		masked bool
	}{
		{"not found", apperror.New(apperror.CodeSheetNotFound, "m", codes.NotFound), true},
		{"permission denied", apperror.New(apperror.CodeForbidden, "m", codes.PermissionDenied), true},
		{"unauthenticated", apperror.New(apperror.CodeUnauthenticated, "m", codes.Unauthenticated), true},
		{"unavailable", apperror.New(apperror.CodeGoogleUnavailable, "m", codes.Unavailable), false},
		{"internal", apperror.New(apperror.CodeUnexpectedError, "m", codes.Internal), false},
		{"not an app error", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNotFoundError(maskNotFound(tc.in)); got != tc.masked {
				t.Errorf("masked = %v, want %v", got, tc.masked)
			}
		})
	}
}

func TestMaskPublicDisabled(t *testing.T) {
	cases := []struct {
		name   string
		in     error
		masked bool
	}{
		{"public disabled", publicDisabled(), true},
		{"another failed precondition", apperror.New(apperror.CodeSheetInvalidTTL, "m", codes.FailedPrecondition), false},
		{"google outage", apperror.New(apperror.CodeGoogleUnavailable, "m", codes.Unavailable), false},
		{"not an app error", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNotFoundError(maskPublicDisabled(tc.in)); got != tc.masked {
				t.Errorf("masked = %v, want %v", got, tc.masked)
			}
		})
	}
}

func TestPublicDisabled(t *testing.T) {
	ae, ok := apperror.AsAppError(publicDisabled())
	if !ok {
		t.Fatal("publicDisabled must produce an AppError so the log names the reason")
	}
	if ae.Code() != apperror.CodeSheetPublicDisabled {
		t.Errorf("code = %q, want %q", ae.Code(), apperror.CodeSheetPublicDisabled)
	}
}
