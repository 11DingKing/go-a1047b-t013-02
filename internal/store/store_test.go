package store

import (
	"errors"
	"sync"
	"testing"
	"time"

	"arcticexpress/internal/domain"
)

func TestStoreSnapshotPersistAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/store.json"

	st1, err := New(path)
	if err != nil {
		t.Fatalf("new 1: %v", err)
	}
	v := domain.NewVoyage("VY-1", "Arctic Sea", "AE-001",
		time.Now().Add(30*24*time.Hour), time.Now().Add(24*24*time.Hour),
		domain.AllPorts, 2, 2)
	b := &domain.Booking{
		ID:        "BK-1",
		State:     domain.StateInitiated,
		VoyageID:  "VY-1",
		Cargo:     domain.Cargo{Type: domain.CargoBattery, Destination: domain.PortRotterdam},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := st1.Mutate(func(tx *Tx) error {
		tx.PutVoyage(v)
		tx.PutBooking(b)
		return nil
	}); err != nil {
		t.Fatalf("mutate: %v", err)
	}

	st2, err := New(path)
	if err != nil {
		t.Fatalf("new 2: %v", err)
	}
	var gotV *domain.Voyage
	var gotB *domain.Booking
	if err := st2.View(func(tx *Tx) error {
		v, ok := tx.Voyage("VY-1")
		if !ok {
			return errors.New("voyage not found after reload")
		}
		gotV = v
		b, ok := tx.Booking("BK-1")
		if !ok {
			return errors.New("booking not found after reload")
		}
		gotB = b
		return nil
	}); err != nil {
		t.Fatalf("view: %v", err)
	}
	if gotV.VoyageNumber != "AE-001" {
		t.Fatalf("voyage number = %s", gotV.VoyageNumber)
	}
	if gotB.State != domain.StateInitiated {
		t.Fatalf("state = %s", gotB.State)
	}
	if gotV.Holds == nil || gotV.Confirmed == nil {
		t.Fatal("voyage maps not initialised after reload")
	}
}

func TestStoreMutateRollbackOnError(t *testing.T) {
	st, err := New("")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	b := &domain.Booking{ID: "BK-1", State: domain.StateInitiated, Cargo: domain.Cargo{Type: domain.CargoBattery}}
	if err := st.Mutate(func(tx *Tx) error {
		tx.PutBooking(b)
		return nil
	}); err != nil {
		t.Fatalf("mutate 1: %v", err)
	}
	want := errors.New("boom")
	err = st.Mutate(func(tx *Tx) error {
		got, _ := tx.Booking("BK-1")
		got.State = domain.StateConfirmed
		tx.PutBooking(got)
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("expected boom error, got %v", err)
	}
	var gotB *domain.Booking
	_ = st.View(func(tx *Tx) error {
		gotB, _ = tx.Booking("BK-1")
		return nil
	})
	if gotB.State != domain.StateInitiated {
		t.Fatalf("state = %s, want initiated (rollback failed)", gotB.State)
	}
}

func TestStoreConcurrentMutateSerialises(t *testing.T) {
	st, err := New("")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	v := domain.NewVoyage("VY-1", "Arctic Sea", "AE-001",
		time.Now().Add(30*24*time.Hour), time.Now().Add(24*24*time.Hour),
		domain.AllPorts, 100, 100)
	_ = st.Mutate(func(tx *Tx) error {
		tx.PutVoyage(v)
		return nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = st.Mutate(func(tx *Tx) error {
				v, ok := tx.Voyage("VY-1")
				if !ok {
					return domain.ErrNotFound
				}
				b := &domain.Booking{
					ID:       tx.NextID("BK"),
					State:    domain.StateInitiated,
					VoyageID: "VY-1",
					Cargo:    domain.Cargo{Type: domain.CargoGeneral, Destination: domain.PortHamburg},
				}
				tx.PutBooking(b)
				v.AddToWaitlist(b.ID)
				tx.PutVoyage(v)
				return nil
			})
		}()
	}
	wg.Wait()

	var count int
	_ = st.View(func(tx *Tx) error {
		count = len(tx.AllBookings())
		return nil
	})
	if count != 50 {
		t.Fatalf("booking count = %d, want 50", count)
	}
}

func TestStoreNextIDMonotonic(t *testing.T) {
	st, err := New("")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	var ids []string
	_ = st.Mutate(func(tx *Tx) error {
		for i := 0; i < 5; i++ {
			ids = append(ids, tx.NextID("BK"))
		}
		return nil
	})
	for i, id := range ids {
		want := "BK-" + itoa(i+1)
		if id != want {
			t.Fatalf("ids[%d] = %s, want %s", i, id, want)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
