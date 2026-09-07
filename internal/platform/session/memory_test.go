package session

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemoryStore_SaveLoad(t *testing.T) {
	s := NewMemoryStore()
	p := Principal{UserID: uuid.New(), Email: "a@b", Source: SourceGenesis}
	if err := s.Save(context.Background(), "sid", p, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Load(context.Background(), "sid")
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if got.Email != "a@b" {
		t.Error("email mismatch")
	}
}

func TestMemoryStore_Missing(t *testing.T) {
	s := NewMemoryStore()
	_, ok, err := s.Load(context.Background(), "nope")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected missing")
	}
}

func TestMemoryStore_Expired(t *testing.T) {
	s := NewMemoryStore()
	_ = s.Save(context.Background(), "sid", Principal{}, time.Now().Add(-time.Second))
	_, ok, _ := s.Load(context.Background(), "sid")
	if ok {
		t.Fatal("expired should return ok=false")
	}
}

func TestMemoryStore_Delete(t *testing.T) {
	s := NewMemoryStore()
	_ = s.Save(context.Background(), "sid", Principal{}, time.Now().Add(time.Hour))
	_ = s.Delete(context.Background(), "sid")
	_, ok, _ := s.Load(context.Background(), "sid")
	if ok {
		t.Fatal("expected deleted")
	}
}
