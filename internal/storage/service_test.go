package storage

import (
	"testing"

	"arcticexpress/internal/domain"
	"arcticexpress/internal/store"
)

func newService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.New("")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return New(st), st
}

func TestStorageReserveConfirmRelease(t *testing.T) {
	s, _ := newService(t)
	sl, err := s.Create(CreateRequest{Zone: "DG-A", Capacity: 2})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := sl.Reserve("BK-1"); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if sl.UsedSlots() != 1 {
		t.Fatalf("used = %d, want 1", sl.UsedSlots())
	}
	sl.Confirm("BK-1")
	if sl.UsedSlots() != 1 {
		t.Fatalf("used after confirm = %d, want 1", sl.UsedSlots())
	}
	if len(sl.Confirmed) != 1 {
		t.Fatalf("confirmed count = %d, want 1", len(sl.Confirmed))
	}
	sl.Release("BK-1")
	if sl.UsedSlots() != 0 {
		t.Fatalf("used after release = %d, want 0", sl.UsedSlots())
	}
}

func TestStorageCapacityExceeded(t *testing.T) {
	s, _ := newService(t)
	sl, _ := s.Create(CreateRequest{Zone: "DG-A", Capacity: 1})
	if err := sl.Reserve("BK-1"); err != nil {
		t.Fatalf("reserve 1: %v", err)
	}
	if err := sl.Reserve("BK-2"); err != domain.ErrNoCapacity {
		t.Fatalf("reserve 2 err = %v, want ErrNoCapacity", err)
	}
}

func TestStorageCreateValidation(t *testing.T) {
	s, _ := newService(t)
	if _, err := s.Create(CreateRequest{Zone: "", Capacity: 5}); err == nil {
		t.Fatal("expected error for empty zone")
	}
	if _, err := s.Create(CreateRequest{Zone: "DG-A", Capacity: 0}); err == nil {
		t.Fatal("expected error for zero capacity")
	}
}

func TestStorageList(t *testing.T) {
	s, _ := newService(t)
	s.Create(CreateRequest{Zone: "DG-A", Capacity: 2})
	s.Create(CreateRequest{Zone: "DG-B", Capacity: 3})
	list, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}
}
