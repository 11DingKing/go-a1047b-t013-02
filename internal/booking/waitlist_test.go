package booking

import (
	"errors"
	"testing"
	"time"

	"arcticexpress/internal/domain"
)

// saturateDG confirms one battery booking so that the voyage's DG capacity of
// one is fully taken, and returns that booking.
func (e *testEnv) saturateDG(t *testing.T, v *domain.Voyage, sl *domain.StorageLocation) *domain.Booking {
	t.Helper()
	b := e.fullDGFlow(t, v, sl, "OWNER-A", "CNTR-A")
	if _, _, err := e.booking.PayDeposit(b.ID, 5000, "DEP-A"); err != nil {
		t.Fatalf("pay deposit A: %v", err)
	}
	return b
}

// TestSaturatedCapacityWaitlistsInsteadOfFailing asserts that a booking asking
// for space on a full voyage joins the waitlist rather than being rejected.
func TestSaturatedCapacityWaitlistsInsteadOfFailing(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 1, 1)
	sl := e.seedStorage(t, 4)
	e.saturateDG(t, v, sl)

	late := e.fullDGFlowNoSpace(t, v, sl, "OWNER-B", "CNTR-B")
	got, err := e.booking.AllocateSpace(late.ID)
	if err != nil {
		t.Fatalf("allocating space on a full voyage must not fail: %v", err)
	}
	if got.State != domain.StateWaitlisted {
		t.Fatalf("state = %s, want waitlisted", got.State)
	}

	report, err := e.voyage.Capacity(v.ID)
	if err != nil {
		t.Fatalf("capacity: %v", err)
	}
	if report.WaitlistLength != 1 {
		t.Fatalf("waitlist length = %d, want 1", report.WaitlistLength)
	}
	if report.AvailableDG != 0 {
		t.Fatalf("available DG = %d, want 0", report.AvailableDG)
	}
}

// fullDGFlowNoSpace drives a battery booking to the storage-verified state,
// stopping short of the space-allocation step.
func (e *testEnv) fullDGFlowNoSpace(t *testing.T, v *domain.Voyage, sl *domain.StorageLocation, owner, container string) *domain.Booking {
	t.Helper()
	b, err := e.booking.Create(CreateBookingRequest{
		IdempotencyKey: container,
		CargoOwnerID:   owner,
		VoyageID:       v.ID,
		ContainerID:    container,
		Cargo:          batteryCargo(domain.PortRotterdam),
	})
	if err != nil {
		t.Fatalf("create %s: %v", container, err)
	}
	cert := &domain.DangerousGoodsCertificate{
		ID:        "DGC-" + container,
		UNNumber:  "UN3480",
		IssuedAt:  e.clock.Now().Add(-24 * time.Hour),
		ExpiresAt: e.clock.Now().Add(30 * 24 * time.Hour),
	}
	pack := &domain.PackingList{Items: []domain.PackingItem{{Description: "battery", Quantity: 1, WeightKg: 1000}}}
	if _, err := e.booking.SubmitDocuments(SubmitDocumentsRequest{
		BookingID: b.ID, ForwarderID: "FWD-1", DGCert: cert, PackingList: pack,
	}); err != nil {
		t.Fatalf("submit docs %s: %v", container, err)
	}
	if _, err := e.booking.VerifyStorage(b.ID, sl.ID); err != nil {
		t.Fatalf("verify storage %s: %v", container, err)
	}
	return b
}

// TestWaitlistedBookingPromotedWhenSpaceFrees asserts the queued booking is
// promoted once the confirmed booking releases its slot.
func TestWaitlistedBookingPromotedWhenSpaceFrees(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 1, 1)
	sl := e.seedStorage(t, 4)
	confirmed := e.saturateDG(t, v, sl)

	late := e.fullDGFlowNoSpace(t, v, sl, "OWNER-B", "CNTR-B")
	if _, err := e.booking.AllocateSpace(late.ID); err != nil {
		t.Fatalf("allocate space: %v", err)
	}

	if _, err := e.booking.Cancel(confirmed.ID, "owner cancelled"); err != nil {
		t.Fatalf("cancel confirmed booking: %v", err)
	}

	promoted, _ := e.booking.Get(late.ID)
	if promoted.State != domain.StateSpaceAllocated {
		t.Fatalf("state = %s, want space_allocated after promotion", promoted.State)
	}
	report, _ := e.voyage.Capacity(v.ID)
	if report.WaitlistLength != 0 {
		t.Fatalf("waitlist length = %d, want 0 after promotion", report.WaitlistLength)
	}
}

// TestMultipleWaitlistedBookingsQueueUp asserts several latecomers all queue.
func TestMultipleWaitlistedBookingsQueueUp(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 1, 1)
	sl := e.seedStorage(t, 6)
	e.saturateDG(t, v, sl)

	for _, container := range []string{"CNTR-B", "CNTR-C", "CNTR-D"} {
		b := e.fullDGFlowNoSpace(t, v, sl, "OWNER-"+container, container)
		got, err := e.booking.AllocateSpace(b.ID)
		if err != nil {
			t.Fatalf("allocate space %s: %v", container, err)
		}
		if got.State != domain.StateWaitlisted {
			t.Fatalf("%s state = %s, want waitlisted", container, got.State)
		}
	}
	report, _ := e.voyage.Capacity(v.ID)
	if report.WaitlistLength != 3 {
		t.Fatalf("waitlist length = %d, want 3", report.WaitlistLength)
	}
}

// TestGeneralCargoPoolIndependentOfDG asserts a saturated DG pool does not
// waitlist general cargo.
func TestGeneralCargoPoolIndependentOfDG(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 1, 1)
	sl := e.seedStorage(t, 4)
	e.saturateDG(t, v, sl)

	gen, err := e.booking.Create(CreateBookingRequest{
		IdempotencyKey: "CNTR-GEN",
		CargoOwnerID:   "OWNER-GEN",
		VoyageID:       v.ID,
		ContainerID:    "CNTR-GEN",
		Cargo: domain.Cargo{
			Description: "pv module",
			Type:        domain.CargoPhotovoltaic,
			WeightKg:    800,
			Destination: domain.PortRotterdam,
		},
	})
	if err != nil {
		t.Fatalf("create general booking: %v", err)
	}
	pack := &domain.PackingList{Items: []domain.PackingItem{{Description: "pv", Quantity: 2, WeightKg: 400}}}
	if _, err := e.booking.SubmitDocuments(SubmitDocumentsRequest{
		BookingID: gen.ID, ForwarderID: "FWD-2", PackingList: pack,
	}); err != nil {
		t.Fatalf("submit docs: %v", err)
	}
	if _, err := e.booking.VerifyStorage(gen.ID, ""); err != nil {
		t.Fatalf("verify storage for general cargo: %v", err)
	}
	got, err := e.booking.AllocateSpace(gen.ID)
	if err != nil {
		t.Fatalf("allocate general space: %v", err)
	}
	if got.State != domain.StateSpaceAllocated {
		t.Fatalf("general cargo state = %s, want space_allocated", got.State)
	}
}

// TestStorageCapacityStillReportsConflict asserts the storage side of the
// no-capacity rule keeps surfacing as a conflict.
func TestStorageCapacityStillReportsConflict(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 4, 4)
	sl := e.seedStorage(t, 1)

	first := e.fullDGFlowNoSpace(t, v, sl, "OWNER-1", "CNTR-1")
	_ = first
	second := e.fullDGFlowNoSpaceExpectError(t, v, sl, "OWNER-2", "CNTR-2")
	if !errors.Is(second, domain.ErrNoCapacity) {
		t.Fatalf("storage reservation err = %v, want ErrNoCapacity", second)
	}
}

// fullDGFlowNoSpaceExpectError returns the error from the storage-verification
// step for a booking that should not fit.
func (e *testEnv) fullDGFlowNoSpaceExpectError(t *testing.T, v *domain.Voyage, sl *domain.StorageLocation, owner, container string) error {
	t.Helper()
	b, err := e.booking.Create(CreateBookingRequest{
		IdempotencyKey: container,
		CargoOwnerID:   owner,
		VoyageID:       v.ID,
		ContainerID:    container,
		Cargo:          batteryCargo(domain.PortRotterdam),
	})
	if err != nil {
		t.Fatalf("create %s: %v", container, err)
	}
	cert := &domain.DangerousGoodsCertificate{
		ID:        "DGC-" + container,
		UNNumber:  "UN3480",
		IssuedAt:  e.clock.Now().Add(-24 * time.Hour),
		ExpiresAt: e.clock.Now().Add(30 * 24 * time.Hour),
	}
	pack := &domain.PackingList{Items: []domain.PackingItem{{Description: "battery", Quantity: 1, WeightKg: 1000}}}
	if _, err := e.booking.SubmitDocuments(SubmitDocumentsRequest{
		BookingID: b.ID, ForwarderID: "FWD-1", DGCert: cert, PackingList: pack,
	}); err != nil {
		t.Fatalf("submit docs %s: %v", container, err)
	}
	_, err = e.booking.VerifyStorage(b.ID, sl.ID)
	return err
}
