package platform_test

import (
	"errors"
	"strings"
	"testing"

	"altalune.id/template/internal/platform"
)

type recordingCloser struct {
	closed bool
	err    error
}

func (c *recordingCloser) Close() error {
	c.closed = true
	return c.err
}

func TestKernel_Close_Nil(t *testing.T) {
	var k *platform.Kernel
	if err := k.Close(); err != nil {
		t.Fatalf("Close(nil): %v", err)
	}
}

func TestKernel_Close_RunsClosersInReverse(t *testing.T) {
	var order []int
	c1, c2, c3 := &recordingCloser{}, &recordingCloser{}, &recordingCloser{}
	wrap := func(c *recordingCloser, i int) fn {
		return func() error {
			order = append(order, i)
			return c.Close()
		}
	}
	k := &platform.Kernel{}
	k.AddCloser(wrap(c1, 1))
	k.AddCloser(wrap(c2, 2))
	k.AddCloser(wrap(c3, 3))
	if err := k.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !c1.closed || !c2.closed || !c3.closed {
		t.Fatal("expected all closers to run")
	}
	want := []int{3, 2, 1}
	if len(order) != len(want) {
		t.Fatalf("order len = %d, want %d", len(order), len(want))
	}
	for i, v := range want {
		if order[i] != v {
			t.Fatalf("order[%d] = %d, want %d (full: %v)", i, order[i], v, order)
		}
	}
}

func TestKernel_Close_JoinsErrors(t *testing.T) {
	e1 := errors.New("boom1")
	e2 := errors.New("boom2")
	k := &platform.Kernel{}
	k.AddCloser(fn(func() error { return e1 }))
	k.AddCloser(fn(func() error { return e2 }))
	err := k.Close()
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, e1) || !errors.Is(err, e2) {
		t.Fatalf("expected both errors in join, got %v", err)
	}
	if !strings.Contains(err.Error(), "boom1") || !strings.Contains(err.Error(), "boom2") {
		t.Fatalf("expected both messages: %v", err)
	}
}

func TestKernel_AddCloser_IgnoresNil(t *testing.T) {
	k := &platform.Kernel{}
	k.AddCloser(nil)
	if err := k.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

type fn func() error

func (f fn) Close() error { return f() }
