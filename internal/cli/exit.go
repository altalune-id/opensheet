package cli

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"

	"altalune.id/template/internal/apperror"
)

const (
	ExitOK                 = 0
	ExitGeneral            = 1
	ExitUnauth             = 2
	ExitForbidden          = 3
	ExitInvalidArg         = 4
	ExitNotFound           = 5
	ExitAlreadyExists      = 6
	ExitOnboardingRequired = 7
	ExitUsage              = 64
)

//nolint:gochecknoglobals // Immutable code-to-exit table; not runtime state.
var exitCodesByGRPC = map[codes.Code]int{
	codes.OK:                 ExitOK,
	codes.Unauthenticated:    ExitUnauth,
	codes.PermissionDenied:   ExitForbidden,
	codes.InvalidArgument:    ExitInvalidArg,
	codes.NotFound:           ExitNotFound,
	codes.AlreadyExists:      ExitAlreadyExists,
	codes.FailedPrecondition: ExitOnboardingRequired,
}

// ExitCodeFor maps err to a stable exit code; unknown errors return 1.
func ExitCodeFor(err error) int {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ExitOK
	}
	ae, ok := apperror.AsAppError(err)
	if !ok {
		return ExitGeneral
	}
	if code, has := exitCodesByGRPC[ae.GRPCCode()]; has {
		return code
	}
	return ExitGeneral
}
