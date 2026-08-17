package voyage

import (
	"testing"
	"time"

	"arcticexpress/internal/domain"
	"arcticexpress/internal/store"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time { return f.t }

func newService(t *testing.T) (*Service, *store.Store, *fakeClock) {
	t.Helper()
	st, err := store.New("")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	clock := &fakeClock{t: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)}
	return New(st, clock), st, clock
}

func mustCreateVoyage(t *testing.T, s *Service) *domain.Voyage {
	t.Helper()
	v, err := s.Create(CreateVoyageRequest{
		VesselName:   "Arctic Sea",
		VoyageNumber: "AE-001",
		DepartureAt:  time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC),
		CutoffAt:     time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC),
		Destinations: domain.AllPorts,
		DGCapacity:   2,
		GenCapacity:  3,
	})
	if err != nil {
		t.Fatalf("create voyage: %v", err)
	}
	return v
}

func TestVoyageCreateValidatesCutoffBeforeDeparture(t *testing.T) {
	s, _, _ := newService(t)
	_, err := s.Create(CreateVoyageRequest{
		VoyageNumber: "AE-001",
		DepartureAt:  time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC),
		CutoffAt:     time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC), // after departure
		Destinations: domain.AllPorts,
		DGCapacity:   2,
		GenCapacity:  3,
	})
	if err == nil {
		t.Fatal("expected error for cutoff after departure")
	}
}

func TestVoyageNoMixingDangerousAndGeneral(t *testing.T) {
	s, st, _ := newService(t)
	v := mustCreateVoyage(t, s)

	// Allocate two DG holds and three general holds — each pool is independent.
	_ = st.Mutate(func(tx *store.Tx) error {
		v, _ := tx.Voyage(v.ID)
		v.AllocateHold("BK-DG-1", domain.Cargo{Type: domain.CargoBattery, Destination: domain.PortRotterdam}, time.Now(), time.Hour)
		v.AllocateHold("BK-DG-2", domain.Cargo{Type: domain.CargoDangerous, Destination: domain.PortHamburg}, time.Now(), time.Hour)
		v.AllocateHold("BK-GEN-1", domain.Cargo{Type: domain.CargoGeneral, Destination: domain.PortGdynia}, time.Now(), time.Hour)
		tx.PutVoyage(v)
		return nil
	})

	report, err := s.Capacity(v.ID)
	if err != nil {
		t.Fatalf("capacity: %v", err)
	}
	if report.HoldsDG != 2 {
		t.Fatalf("holds DG = %d, want 2", report.HoldsDG)
	}
	if report.HoldsGen != 1 {
		t.Fatalf("holds Gen = %d, want 1", report.HoldsGen)
	}
	// Available reflects confirmed bookings only; tentative holds do not
	// reduce it because holds may overlap and expire.
	if report.AvailableDG != 2 {
		t.Fatalf("available DG = %d, want 2", report.AvailableDG)
	}
	if report.AvailableGen != 3 {
		t.Fatalf("available Gen = %d, want 3", report.AvailableGen)
	}
}

func TestVoyageWaitlistAndPromotion(t *testing.T) {
	s, st, _ := newService(t)
	v := mustCreateVoyage(t, s)
	v.DGCapacity = 1

	_ = st.Mutate(func(tx *store.Tx) error {
		v, _ := tx.Voyage(v.ID)
		v.AllocateHold("BK-1", domain.Cargo{Type: domain.CargoBattery, Destination: domain.PortRotterdam}, time.Now(), time.Hour)
		v.ConfirmSpace("BK-1", time.Now())
		tx.PutVoyage(v)
		return nil
	})

	// Capacity full → second goes to waitlist.
	_ = st.Mutate(func(tx *store.Tx) error {
		v, _ := tx.Voyage(v.ID)
		v.AddToWaitlist("BK-2")
		tx.PutVoyage(v)
		return nil
	})
	report, _ := s.Capacity(v.ID)
	if report.WaitlistLength != 1 {
		t.Fatalf("waitlist = %d, want 1", report.WaitlistLength)
	}

	// Release → promote.
	_ = st.Mutate(func(tx *store.Tx) error {
		v, _ := tx.Voyage(v.ID)
		v.ReleaseSpace("BK-1")
		tx.PutVoyage(v)
		return nil
	})
	report, _ = s.Capacity(v.ID)
	if report.AvailableDG != 1 {
		t.Fatalf("available DG = %d, want 1", report.AvailableDG)
	}
}

func TestVoyageExpiredHoldsDetected(t *testing.T) {
	s, st, clock := newService(t)
	v := mustCreateVoyage(t, s)

	now := clock.Now()
	_ = st.Mutate(func(tx *store.Tx) error {
		v, _ := tx.Voyage(v.ID)
		v.AllocateHold("BK-1", domain.Cargo{Type: domain.CargoBattery, Destination: domain.PortRotterdam}, now, 100*time.Millisecond)
		tx.PutVoyage(v)
		return nil
	})

	clock.t = now.Add(200 * time.Millisecond)
	var expired []string
	_ = st.View(func(tx *store.Tx) error {
		v, _ := tx.Voyage(v.ID)
		expired = v.ExpiredHolds(clock.Now())
		return nil
	})
	if len(expired) != 1 || expired[0] != "BK-1" {
		t.Fatalf("expired = %v, want [BK-1]", expired)
	}
}
