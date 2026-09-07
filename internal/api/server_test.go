package api_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	authv1 "altalune.id/template/gen/go/auth/v1"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/reqid"
)

func TestServer_Chain_RoundTrip_EchoesRequestID(t *testing.T) {
	userID := uuid.New()
	h := newHarness(t, session.Principal{UserID: userID, Email: "a@b"})

	req := connect.NewRequest(&authv1.WhoamiRequest{})
	withBearer(req.Header())
	req.Header().Set(reqid.Header, "test-abc")

	resp, err := h.whoamiClient().Whoami(context.Background(), req)
	if err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if got := resp.Header().Get(reqid.Header); got != "test-abc" {
		t.Errorf("response reqid = %q, want %q", got, "test-abc")
	}
}

func TestServer_Chain_RoundTrip_GeneratesRequestIDWhenMissing(t *testing.T) {
	userID := uuid.New()
	h := newHarness(t, session.Principal{UserID: userID, Email: "a@b"})

	req := connect.NewRequest(&authv1.WhoamiRequest{})
	withBearer(req.Header())

	resp, err := h.whoamiClient().Whoami(context.Background(), req)
	if err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if got := resp.Header().Get(reqid.Header); got == "" {
		t.Error("response should carry a generated reqid header")
	}
}

func TestServer_Chain_ErrorPathAttachesRequestID(t *testing.T) {
	h := newHarness(t, session.Principal{UserID: uuid.New(), Email: "a@b"})

	req := connect.NewRequest(&authv1.WhoamiRequest{})
	// no bearer — Auth interceptor rejects → error path
	_, err := h.whoamiClient().Whoami(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	var cerr *connect.Error
	if !isConnectErr(err, &cerr) {
		t.Fatalf("expected *connect.Error, got %T", err)
	}
	if got := cerr.Meta().Get(reqid.Header); got == "" {
		t.Error("connect.Error meta missing reqid header")
	}
}

func isConnectErr(err error, out **connect.Error) bool {
	return errors.As(err, out)
}
