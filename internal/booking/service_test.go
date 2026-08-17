package booking

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"arcticexpress/internal/domain"
	"arcticexpress/internal/payment"
	"arcticexpress/internal/storage"
	"arcticexpress/internal/store"
	"arcticexpress/internal/voyage"
)

// fakeClock is a controllable Clock for deterministic tests.
type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock                          { return &fakeClock{t: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)} }
func (f *fakeClock) Now() time.Time                     { return f.t }
func (f *fakeClock) Advance(d time.Duration) *fakeClock { f.t = f.t.Add(d); return f }

// testEnv bundles services with a shared store and clock.
type testEnv struct {
	store   *store.Store
	clock   *fakeClock
	booking *Service
	voyage  *voyage.Service
	storage *storage.Service
	payment *payment.Service
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	st, err := store.New("")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	clock := newFakeClock()
	cfg := Config{HoldDuration: 200 * time.Millisecond, CutoffThreshold: 48 * time.Hour, PortChangeWindow: 72 * time.Hour}
	return &testEnv{
		store:   st,
		clock:   clock,
		booking: New(st, clock, cfg),
		voyage:  voyage.New(st, clock),
		storage: storage.New(st),
		payment: payment.New(st, clock),
	}
}

// seedVoyage creates a voyage departing far in the future with the given
// capacities so that port-change and cutoff rules are satisfiable.
func (e *testEnv) seedVoyage(t *testing.T, dgCap, genCap int) *domain.Voyage {
	t.Helper()
	departure := e.clock.Now().Add(30 * 24 * time.Hour)
	cutoff := departure.Add(-6 * 24 * time.Hour)
	v, err := e.voyage.Create(voyage.CreateVoyageRequest{
		VesselName:   "Arctic Sea",
		VoyageNumber: "AE-001",
		DepartureAt:  departure,
		CutoffAt:     cutoff,
		Destinations: domain.AllPorts,
		DGCapacity:   dgCap,
		GenCapacity:  genCap,
	})
	if err != nil {
		t.Fatalf("create voyage: %v", err)
	}
	return v
}

func (e *testEnv) seedStorage(t *testing.T, capacity int) *domain.StorageLocation {
	t.Helper()
	sl, err := e.storage.Create(storage.CreateRequest{Zone: "DG-A", Capacity: capacity})
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	return sl
}

func batteryCargo(dest domain.DestinationPort) domain.Cargo {
	return domain.Cargo{
		Description: "power battery",
		Type:        domain.CargoBattery,
		WeightKg:    1000,
		UNNumber:    "UN3480",
		Destination: dest,
	}
}

// fullDGFlow drives a battery booking through create → docs → storage → space.
func (e *testEnv) fullDGFlow(t *testing.T, v *domain.Voyage, sl *domain.StorageLocation, owner, container string) *domain.Booking {
	t.Helper()
	b, err := e.booking.Create(CreateBookingRequest{
		IdempotencyKey: container,
		CargoOwnerID:   owner,
		VoyageID:       v.ID,
		ContainerID:    container,
		Cargo:          batteryCargo(domain.PortRotterdam),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	cert := &domain.DangerousGoodsCertificate{
		ID:        "DGC-" + container,
		UNNumber:  "UN3480",
		IssuedAt:  e.clock.Now().Add(-24 * time.Hour),
		ExpiresAt: e.clock.Now().Add(30 * 24 * time.Hour),
	}
	pack := &domain.PackingList{Items: []domain.PackingItem{{Description: "battery", Quantity: 1, WeightKg: 1000}}}
	if _, err := e.booking.SubmitDocuments(SubmitDocumentsRequest{BookingID: b.ID, ForwarderID: "FWD-1", DGCert: cert, PackingList: pack}); err != nil {
		t.Fatalf("submit docs: %v", err)
	}
	if _, err := e.booking.VerifyStorage(b.ID, sl.ID); err != nil {
		t.Fatalf("verify storage: %v", err)
	}
	if _, err := e.booking.AllocateSpace(b.ID); err != nil {
		t.Fatalf("allocate space: %v", err)
	}
	return b
}

func TestBookingFullLifecycleNormalPath(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 2, 2)
	sl := e.seedStorage(t, 2)

	b := e.fullDGFlow(t, v, sl, "OWNER-1", "CNTR-1")

	b2, dep, err := e.booking.PayDeposit(b.ID, 5000, "DEP-KEY-1")
	if err != nil {
		t.Fatalf("pay deposit: %v", err)
	}
	if b2.State != domain.StateDepositPaid {
		t.Fatalf("state = %s, want deposit_paid", b2.State)
	}
	if dep.Status != domain.DepositConfirmed {
		t.Fatalf("deposit status = %s, want confirmed", dep.Status)
	}

	if _, err := e.booking.BindBill(b.ID, "BL-001", "CONS-1"); err != nil {
		t.Fatalf("bind bill: %v", err)
	}
	if _, err := e.booking.CustomsRelease(b.ID); err != nil {
		t.Fatalf("customs release: %v", err)
	}
	b3, err := e.booking.Confirm(b.ID)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if b3.State != domain.StateConfirmed {
		t.Fatalf("state = %s, want confirmed", b3.State)
	}

	got, _ := e.booking.Get(b.ID)
	if !got.StorageLocked {
		t.Fatal("storage should remain locked after confirmation")
	}
}

func TestBookingIdempotentCreate(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 1, 1)
	req := CreateBookingRequest{
		IdempotencyKey: "IDEM-1",
		CargoOwnerID:   "OWNER-1",
		VoyageID:       v.ID,
		ContainerID:    "CNTR-1",
		Cargo:          batteryCargo(domain.PortRotterdam),
	}
	b1, err := e.booking.Create(req)
	if err != nil {
		t.Fatalf("create 1: %v", err)
	}
	b2, err := e.booking.Create(req)
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	if b1.ID != b2.ID {
		t.Fatalf("idempotent create returned different bookings: %s vs %s", b1.ID, b2.ID)
	}
}

func TestBookingContainerAlreadyBound(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 2, 2)
	sl := e.seedStorage(t, 2)
	b := e.fullDGFlow(t, v, sl, "OWNER-1", "CNTR-1")

	_, err := e.booking.Create(CreateBookingRequest{
		CargoOwnerID: "OWNER-2",
		VoyageID:     v.ID,
		ContainerID:  b.ContainerID,
		Cargo:        batteryCargo(domain.PortRotterdam),
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestBookingDangerousRequiresDGCert(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 1, 1)
	b, err := e.booking.Create(CreateBookingRequest{
		CargoOwnerID: "OWNER-1",
		VoyageID:     v.ID,
		ContainerID:  "CNTR-1",
		Cargo:        batteryCargo(domain.PortRotterdam),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = e.booking.SubmitDocuments(SubmitDocumentsRequest{
		BookingID:   b.ID,
		ForwarderID: "FWD-1",
		PackingList: &domain.PackingList{Items: []domain.PackingItem{{Description: "x", Quantity: 1, WeightKg: 1000}}},
	})
	if !errors.Is(err, domain.ErrMissingDocs) {
		t.Fatalf("expected ErrMissingDocs, got %v", err)
	}
}

func TestBookingInvalidStateTransition(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 1, 1)
	b, err := e.booking.Create(CreateBookingRequest{
		CargoOwnerID: "OWNER-1",
		VoyageID:     v.ID,
		ContainerID:  "CNTR-1",
		Cargo:        batteryCargo(domain.PortRotterdam),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := e.booking.PayDeposit(b.ID, 5000, "KEY-1"); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState, got %v", err)
	}
	if _, err := e.booking.AllocateSpace(b.ID); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState, got %v", err)
	}
}

func TestBookingConcurrentDepositFirstWins(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 1, 1)
	sl := e.seedStorage(t, 2)

	bA := e.fullDGFlow(t, v, sl, "OWNER-A", "CNTR-A")
	bB := e.fullDGFlow(t, v, sl, "OWNER-B", "CNTR-B")

	var wg sync.WaitGroup
	var win, waitl atomic.Int32
	for _, bk := range []*domain.Booking{bA, bB} {
		wg.Add(1)
		go func(bk *domain.Booking) {
			defer wg.Done()
			_, _, err := e.booking.PayDeposit(bk.ID, 5000, fmt.Sprintf("DEP-%s", bk.ID))
			if err != nil {
				t.Errorf("pay deposit %s: %v", bk.ID, err)
				return
			}
			updated, _ := e.booking.Get(bk.ID)
			switch updated.State {
			case domain.StateDepositPaid:
				win.Add(1)
			case domain.StateWaitlisted:
				waitl.Add(1)
			}
		}(bk)
	}
	wg.Wait()

	if win.Load() != 1 {
		t.Fatalf("expected 1 winner, got %d", win.Load())
	}
	if waitl.Load() != 1 {
		t.Fatalf("expected 1 waitlisted, got %d", waitl.Load())
	}

	// Identify the loser and verify the deposit was refunded.
	gotA, _ := e.booking.Get(bA.ID)
	loserID := bB.ID
	if gotA.State == domain.StateWaitlisted {
		loserID = bA.ID
	}
	dep, err := e.payment.GetByBooking(loserID)
	if err != nil {
		t.Fatalf("get loser deposit: %v", err)
	}
	if dep.Status != domain.DepositRefunded {
		t.Fatalf("loser deposit status = %s, want refunded", dep.Status)
	}
}

func TestBookingCancelRollback(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 2, 2)
	sl := e.seedStorage(t, 2)
	b := e.fullDGFlow(t, v, sl, "OWNER-1", "CNTR-1")

	if _, _, err := e.booking.PayDeposit(b.ID, 5000, "DEP-1"); err != nil {
		t.Fatalf("pay deposit: %v", err)
	}

	cancelled, err := e.booking.Cancel(b.ID, "cargo owner cancel")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.State != domain.StateCancelled {
		t.Fatalf("state = %s, want cancelled", cancelled.State)
	}

	report, _ := e.voyage.Capacity(v.ID)
	if report.AvailableDG != 2 {
		t.Fatalf("available DG = %d, want 2", report.AvailableDG)
	}
	slGot, _ := e.storage.Get(sl.ID)
	if slGot.UsedSlots() != 0 {
		t.Fatalf("storage used = %d, want 0", slGot.UsedSlots())
	}
	dep, _ := e.payment.GetByBooking(b.ID)
	if dep.Status != domain.DepositRefunded {
		t.Fatalf("deposit status = %s, want refunded", dep.Status)
	}
	got, _ := e.booking.Get(b.ID)
	if !got.HasDGCertificate() || !got.HasPackingList() {
		t.Fatal("docs should be retained after cancellation")
	}
}

func TestBookingWaitlistPromotion(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 1, 1)
	sl := e.seedStorage(t, 2)

	bA := e.fullDGFlow(t, v, sl, "OWNER-A", "CNTR-A")
	if _, _, err := e.booking.PayDeposit(bA.ID, 5000, "DEP-A"); err != nil {
		t.Fatalf("pay deposit A: %v", err)
	}

	// Second booking hits full capacity → waitlisted.
	bB := e.fullDGFlow(t, v, sl, "OWNER-B", "CNTR-B")
	gotB, _ := e.booking.Get(bB.ID)
	if gotB.State != domain.StateWaitlisted {
		t.Fatalf("B state = %s, want waitlisted", gotB.State)
	}

	// Cancel A → B should be promoted.
	if _, err := e.booking.Cancel(bA.ID, "cancel A"); err != nil {
		t.Fatalf("cancel A: %v", err)
	}

	gotB2, _ := e.booking.Get(bB.ID)
	if gotB2.State != domain.StateSpaceAllocated {
		t.Fatalf("B state = %s, want space_allocated after promotion", gotB2.State)
	}
}

func TestBookingDepositTimeoutRelease(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 1, 1)
	sl := e.seedStorage(t, 1)
	b := e.fullDGFlow(t, v, sl, "OWNER-1", "CNTR-1")

	// Hold duration is 200 ms. Advance past it without paying.
	e.clock.Advance(300 * time.Millisecond)
	if err := e.booking.CancelExpiredHold(b.ID); err != nil {
		t.Fatalf("cancel expired: %v", err)
	}
	got, _ := e.booking.Get(b.ID)
	if got.State != domain.StateCancelled {
		t.Fatalf("state = %s, want cancelled", got.State)
	}
	// Space should be free again.
	report, _ := e.voyage.Capacity(v.ID)
	if report.AvailableDG != 1 {
		t.Fatalf("available DG = %d, want 1", report.AvailableDG)
	}
}

func TestBookingCustomsRejectRecovery(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 1, 1)
	sl := e.seedStorage(t, 1)
	b := e.fullDGFlow(t, v, sl, "OWNER-1", "CNTR-1")
	if _, _, err := e.booking.PayDeposit(b.ID, 5000, "DEP-1"); err != nil {
		t.Fatalf("pay deposit: %v", err)
	}
	if _, err := e.booking.BindBill(b.ID, "BL-1", "CONS-1"); err != nil {
		t.Fatalf("bind bill: %v", err)
	}
	if _, err := e.booking.CustomsRelease(b.ID); err != nil {
		t.Fatalf("customs release: %v", err)
	}

	// Customs rejects → rollback + pending review.
	rejected, err := e.booking.CustomsReject(b.ID, "inspection failed")
	if err != nil {
		t.Fatalf("customs reject: %v", err)
	}
	if rejected.State != domain.StatePendingReview {
		t.Fatalf("state = %s, want pending_review", rejected.State)
	}
	report, _ := e.voyage.Capacity(v.ID)
	if report.AvailableDG != 1 {
		t.Fatalf("available DG = %d, want 1 after rollback", report.AvailableDG)
	}
	dep, _ := e.payment.GetByBooking(b.ID)
	if dep.Status != domain.DepositRefunded {
		t.Fatalf("deposit = %s, want refunded", dep.Status)
	}
	got, _ := e.booking.Get(b.ID)
	if !got.HasDGCertificate() || !got.HasPackingList() {
		t.Fatal("docs should be retained after rejection")
	}
	if got.RequeueCount != 0 {
		t.Fatalf("requeue count = %d, want 0", got.RequeueCount)
	}

	// Specialist re-queues → docs retained so state goes straight to docs_submitted.
	rq, err := e.booking.Requeue(b.ID)
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if rq.State != domain.StateDocsSubmitted {
		t.Fatalf("state = %s, want docs_submitted", rq.State)
	}
	if rq.RequeueCount != 1 {
		t.Fatalf("requeue count = %d, want 1", rq.RequeueCount)
	}
}

func TestBookingPortChangeWindow(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 1, 1)
	sl := e.seedStorage(t, 1)
	b := e.fullDGFlow(t, v, sl, "OWNER-1", "CNTR-1")

	// Far from departure → allowed.
	if _, err := e.booking.ChangePort(b.ID, domain.PortHamburg); err != nil {
		t.Fatalf("change port early: %v", err)
	}
	got, _ := e.booking.Get(b.ID)
	if got.Destination != domain.PortHamburg {
		t.Fatalf("destination = %s, want Hamburg", got.Destination)
	}

	// Advance to within 72 h of departure → blocked.
	departure := v.DepartureAt
	e.clock.t = departure.Add(-24 * time.Hour)
	_, err := e.booking.ChangePort(b.ID, domain.PortGdynia)
	if !errors.Is(err, domain.ErrWindowClosed) {
		t.Fatalf("expected ErrWindowClosed, got %v", err)
	}
}

func TestBookingOneContainerOneBill(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 4, 4)
	sl := e.seedStorage(t, 4)
	b := e.fullDGFlow(t, v, sl, "OWNER-1", "CNTR-1")
	b2 := e.fullDGFlow(t, v, sl, "OWNER-2", "CNTR-2")

	if _, err := e.booking.BindBill(b.ID, "BL-1", "CONS-1"); err != nil {
		t.Fatalf("bind bill 1: %v", err)
	}
	// Different container, different B/L — fine.
	if _, err := e.booking.BindBill(b2.ID, "BL-2", "CONS-2"); err != nil {
		t.Fatalf("bind bill 2: %v", err)
	}
	// Try to bind a second B/L to the same container via a new booking with
	// the same container id — already prevented at Create. But also verify
	// that we cannot reuse the same B/L across two active bookings.
	b3 := e.fullDGFlow(t, v, sl, "OWNER-3", "CNTR-3")
	_, err := e.booking.BindBill(b3.ID, "BL-1", "CONS-3")
	if err == nil {
		t.Fatal("expected error binding duplicate B/L to different container")
	}
}

func TestBookingIdempotentDeposit(t *testing.T) {
	e := newTestEnv(t)
	v := e.seedVoyage(t, 1, 1)
	sl := e.seedStorage(t, 1)
	b := e.fullDGFlow(t, v, sl, "OWNER-1", "CNTR-1")

	_, dep1, err := e.booking.PayDeposit(b.ID, 5000, "IDEM-DEP")
	if err != nil {
		t.Fatalf("pay deposit 1: %v", err)
	}
	_, dep2, err := e.booking.PayDeposit(b.ID, 5000, "IDEM-DEP")
	if err != nil {
		t.Fatalf("pay deposit 2: %v", err)
	}
	if dep1.ID != dep2.ID {
		t.Fatalf("idempotent deposit returned different deposits: %s vs %s", dep1.ID, dep2.ID)
	}
}
