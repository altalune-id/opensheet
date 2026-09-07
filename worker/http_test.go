package worker

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func listenLocal(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return addr
}

func waitReady(t *testing.T, url string) *http.Response {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			return resp
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server never became ready at %s", url)
	return nil
}

func TestHTTP_ServesAndDrainsOnCancel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hi"))
	})

	addr := listenLocal(t)
	w := HTTP("test", addr, mux, newTestLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- w.Run(ctx) }()

	resp := waitReady(t, "http://"+addr+"/hello")
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "hi" {
		t.Fatalf("body=%q, want hi", string(body))
	}

	cancel()
	select {
	case err := <-runErrCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err=%v, want nil or context.Canceled", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("worker did not stop after cancel")
	}
}

func TestHTTP_ListenErrorIsReturned(t *testing.T) {
	w := HTTP("dup", "127.0.0.1:-1", http.NewServeMux(), newTestLogger())
	err := w.Run(context.Background())
	if err == nil {
		t.Fatal("expected listen error on bogus port")
	}
}

func TestHTTP_Name(t *testing.T) {
	w := HTTP("metrics", "127.0.0.1:0", http.NewServeMux(), nil)
	if w.Name() != "metrics" {
		t.Fatalf("Name=%q, want metrics", w.Name())
	}
}
