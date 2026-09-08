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

func TestMemoryStore_DeleteExpiredRemovesOnlyExpiredRows(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	live := uuid.Must(uuid.NewV7())
	if err := s.Save(ctx, "live", Principal{UserID: live}, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("save live: %v", err)
	}
	if err := s.Save(ctx, "dead", Principal{}, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("save dead: %v", err)
	}

	n, err := s.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if n != 1 {
		t.Fatalf("deleted = %d, want 1", n)
	}

	if p, ok, lErr := s.Load(ctx, "live"); lErr != nil || !ok || p.UserID != live {
		t.Fatalf("live session lost: p=%v ok=%v err=%v", p.UserID, ok, lErr)
	}
	if _, ok, lErr := s.Load(ctx, "dead"); lErr != nil || ok {
		t.Fatalf("expired session survived: ok=%v err=%v", ok, lErr)
	}
}
