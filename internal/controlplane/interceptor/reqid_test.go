package interceptor_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"altalune.id/opensheet/internal/controlplane/interceptor"
	"altalune.id/opensheet/reqid"
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

func TestRequestID_SanitizesInboundHeader(t *testing.T) {
	const uuidV7 = "0192a3f1-c7c1-7c1d-b1d1-abcdef012345"
	uuidV7Pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for _, tt := range []struct {
		name       string
		inbound    string
		sets       bool
		propagated bool
	}{
		{name: "uuid v7 is propagated", inbound: uuidV7, sets: true, propagated: true},
		{name: "absent mints a fresh id", sets: false},
		{name: "empty mints a fresh id", inbound: "", sets: true},
		{name: "oversized is replaced", inbound: strings.Repeat("A", reqid.MaxLength+1), sets: true},
		{name: "control characters are replaced", inbound: "abc\x00\x1bdef", sets: true},
		{name: "newline injection is replaced", inbound: "abc\n{\"msg\":\"forged\"}", sets: true},
		{name: "whitespace is replaced", inbound: "abc def", sets: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := newReq()
			if tt.sets {
				req.Header().Set(reqid.Header, tt.inbound)
			}
			var inCtx string
			next := connect.UnaryFunc(func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
				inCtx = reqid.FromContext(ctx)
				return connect.NewResponse(&struct{}{}), nil
			})
			resp, err := interceptor.RequestID()(next)(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			echoed := resp.Header().Get(reqid.Header)
			if tt.propagated && echoed != tt.inbound {
				t.Errorf("echoed = %q, want the inbound %q", echoed, tt.inbound)
			}
			if !tt.propagated {
				if echoed == tt.inbound {
					t.Fatalf("echoed the unsafe inbound value %q verbatim", tt.inbound)
				}
				if !uuidV7Pattern.MatchString(echoed) {
					t.Fatalf("echoed = %q, want a freshly minted UUIDv7", echoed)
				}
			}
			if inCtx != echoed {
				t.Errorf("ctx id = %q, echoed = %q; they must agree", inCtx, echoed)
			}
		})
	}
}

// NOTE: reusing the listener's BaseContext id would make every RPC in a deployment share one id.
func TestRequestIDDoesNotInheritTheBaseContextID(t *testing.T) {
	base := reqid.WithContext(t.Context(), "process-wide-id")
	inter := interceptor.RequestID()
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&struct{}{}), nil
	})

	seen := map[string]bool{}
	for range 3 {
		resp, err := inter(next)(base, newReq())
		if err != nil {
			t.Fatal(err)
		}
		got := resp.Header().Get(reqid.Header)
		if got == "process-wide-id" {
			t.Fatalf("response reused the base context id %q", got)
		}
		if seen[got] {
			t.Fatalf("request id %q was reused across calls", got)
		}
		seen[got] = true
	}

	req := newReq()
	req.Header().Set(reqid.Header, "caller-supplied")
	resp, err := inter(next)(base, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header().Get(reqid.Header); got != "caller-supplied" {
		t.Fatalf("inbound id = %q, want %q", got, "caller-supplied")
	}
}
