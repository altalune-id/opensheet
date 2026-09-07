package interceptor_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/api/interceptor"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/reqid"
)

func TestWrap_AppErrorConvertsToConnectError(t *testing.T) {
	inter := interceptor.Wrap(nil)
	req := newReq()
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, apperror.New("test.not_found", "not found", codes.NotFound,
			&apperrorv1.ErrorDetail{Code: "test.not_found"})
	})
	_, err := inter(next)(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("expected *connect.Error, got %T", err)
	}
	if cerr.Code() != connect.CodeNotFound {
		t.Errorf("code = %v, want NotFound", cerr.Code())
	}
	if got := len(cerr.Details()); got != 1 {
		t.Fatalf("details len = %d, want 1", got)
	}
}

func TestWrap_AttachesRequestIDIntoErrorDetail(t *testing.T) {
	inter := interceptor.Wrap(nil)
	req := newReq()
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, apperror.New("test.code", "msg", codes.InvalidArgument,
			&apperrorv1.ErrorDetail{Code: "test.code"})
	})
	ctx := reqid.WithContext(context.Background(), "req-abc")
	_, err := inter(next)(ctx, req)
	if err == nil {
		t.Fatal("expected error")
	}
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatal("expected connect error")
	}
	// The apperror.AppError under connect.Error should carry request_id in its
	// ErrorDetail after AttachContext runs.
	ae, ok := apperror.AsAppError(err)
	if !ok {
		t.Fatal("expected AsAppError to find AppError")
	}
	details := ae.Details()
	if len(details) != 1 {
		t.Fatalf("details len = %d", len(details))
	}
	ed, ok := details[0].(*apperrorv1.ErrorDetail)
	if !ok {
		t.Fatalf("unexpected detail type %T", details[0])
	}
	if ed.RequestId != "req-abc" {
		t.Errorf("RequestId = %q, want req-abc", ed.RequestId)
	}
}

func TestWrap_UnknownErrorRoutesThroughUnexpected(t *testing.T) {
	called := false
	unexpected := func(_ context.Context, _ string, cause error, _ ...any) *apperror.AppError {
		called = true
		return apperror.New(apperror.CodeUnexpectedError, cause.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: apperror.CodeUnexpectedError}).WithCause(cause)
	}
	inter := interceptor.Wrap(unexpected)
	req := newReq()
	raw := errors.New("boom")
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, raw
	})
	_, err := inter(next)(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	if !called {
		t.Error("unexpected() was not invoked")
	}
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("expected *connect.Error, got %T", err)
	}
	if cerr.Code() != connect.CodeInternal {
		t.Errorf("code = %v, want Internal", cerr.Code())
	}
}

func TestWrap_PassesThroughSuccess(t *testing.T) {
	inter := interceptor.Wrap(nil)
	req := newReq()
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&struct{}{}), nil
	})
	resp, err := inter(next)(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
}
