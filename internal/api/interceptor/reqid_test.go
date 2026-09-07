package interceptor_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"

	"altalune.id/template/internal/api/interceptor"
	"altalune.id/template/reqid"
)

type emptyReq struct{}

func newReq() *connect.Request[emptyReq] { return connect.NewRequest(&emptyReq{}) }

func TestRequestID_GeneratesWhenAbsent(t *testing.T) {
	inter := interceptor.RequestID()
	req := newReq()
	var gotCtx context.Context
	next := connect.UnaryFunc(func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		gotCtx = ctx
		return connect.NewResponse(&struct{}{}), nil
	})
	resp, err := inter(next)(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	inCtx := reqid.FromContext(gotCtx)
	if inCtx == "" {
		t.Fatal("interceptor did not inject a request-id into ctx")
	}
	if got := resp.Header().Get(reqid.Header); got != inCtx {
		t.Errorf("response header %q, ctx %q", got, inCtx)
	}
}

func TestRequestID_UsesInboundHeader(t *testing.T) {
	inter := interceptor.RequestID()
	req := newReq()
	req.Header().Set(reqid.Header, "trace-42")
	next := connect.UnaryFunc(func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		if got := reqid.FromContext(ctx); got != "trace-42" {
			t.Errorf("ctx reqid = %q, want trace-42", got)
		}
		return connect.NewResponse(&struct{}{}), nil
	})
	resp, err := inter(next)(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header().Get(reqid.Header); got != "trace-42" {
		t.Errorf("response header = %q, want trace-42", got)
	}
}

func TestRequestID_ErrorPath_StampsConnectErrorMeta(t *testing.T) {
	inter := interceptor.RequestID()
	req := newReq()
	req.Header().Set(reqid.Header, "err-77")
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, connect.NewError(connect.CodeInternal, errors.New("boom"))
	})
	_, err := inter(next)(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("expected *connect.Error, got %T", err)
	}
	if got := cerr.Meta().Get(reqid.Header); got != "err-77" {
		t.Errorf("connect.Error meta reqid = %q, want err-77", got)
	}
}
