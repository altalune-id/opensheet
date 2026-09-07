package cli

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apperror"
)

func TestExitCodeFor_Nil(t *testing.T) {
	if got := ExitCodeFor(nil); got != ExitOK {
		t.Fatalf("want 0, got %d", got)
	}
}

func TestExitCodeFor_Unknown(t *testing.T) {
	if got := ExitCodeFor(errors.New("boom")); got != ExitGeneral {
		t.Fatalf("want %d, got %d", ExitGeneral, got)
	}
}

func TestExitCodeFor_GRPCMapping(t *testing.T) {
	cases := []struct {
		name string
		code codes.Code
		want int
	}{
		{"unauthenticated", codes.Unauthenticated, ExitUnauth},
		{"forbidden", codes.PermissionDenied, ExitForbidden},
		{"invalid-arg", codes.InvalidArgument, ExitInvalidArg},
		{"not-found", codes.NotFound, ExitNotFound},
		{"already-exists", codes.AlreadyExists, ExitAlreadyExists},
		{"other-maps-to-1", codes.Internal, ExitGeneral},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := apperror.New("X", "msg", tc.code, &apperrorv1.ErrorDetail{Code: "X"})
			if got := ExitCodeFor(err); got != tc.want {
				t.Errorf("want %d, got %d", tc.want, got)
			}
		})
	}
}
