// Package apperror defines the canonical error envelope crossing every layer.
package apperror

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
)

// AppError is the canonical error envelope.
type AppError struct {
	code     string
	message  string
	grpcCode codes.Code
	details  []proto.Message
	cause    error
	upstream *apperrorv1.UpstreamErrorDetail
}

// New builds an AppError with the given code, human message, gRPC code, and optional details.
func New(code, message string, grpcCode codes.Code, details ...proto.Message) *AppError {
	return &AppError{
		code:     code,
		message:  message,
		grpcCode: grpcCode,
		details:  details,
	}
}

func (e *AppError) Error() string        { return e.message }
func (e *AppError) Unwrap() error        { return e.cause }
func (e *AppError) Code() string         { return e.code }
func (e *AppError) Message() string      { return e.message }
func (e *AppError) GRPCCode() codes.Code { return e.grpcCode }

// Details returns a defensive copy so callers can't mutate the envelope.
func (e *AppError) Details() []proto.Message {
	return append([]proto.Message(nil), e.details...)
}

// Upstream returns the immediate upstream provenance, or nil.
func (e *AppError) Upstream() *apperrorv1.UpstreamErrorDetail { return e.upstream }

// WithCause attaches the wrapped root cause; enables errors.Is / errors.As.
func (e *AppError) WithCause(err error) *AppError {
	if err == nil {
		return e
	}
	cp := *e
	cp.cause = err
	return &cp
}

// WithUpstream attaches one-hop upstream provenance. Nil is a no-op.
func (e *AppError) WithUpstream(u *apperrorv1.UpstreamErrorDetail) *AppError {
	if u == nil {
		return e
	}
	cp := *e
	cp.upstream = u
	return &cp
}
