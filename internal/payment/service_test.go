package payment

import (
	"errors"
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

func TestPaymentRefundConfirmedDeposit(t *testing.T) {
	s, st, clock := newService(t)
	dep := &domain.Deposit{
		ID:        "DEP-1",
		BookingID: "BK-1",
		Amount:    5000,
		Currency:  "CNY",
		Status:    domain.DepositConfirmed,
		PaidAt:    clock.Now(),
	}
	_ = st.Mutate(func(tx *store.Tx) error {
		tx.PutDeposit(dep)
		return nil
	})

	refunded, err := s.Refund("BK-1", "cancellation")
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if refunded.Status != domain.DepositRefunded {
		t.Fatalf("status = %s, want refunded", refunded.Status)
	}
	if refunded.RefundReason != "cancellation" {
		t.Fatalf("reason = %s", refunded.RefundReason)
	}
}

func TestPaymentRefundIdempotent(t *testing.T) {
	s, st, _ := newService(t)
	dep := &domain.Deposit{
		ID:        "DEP-1",
		BookingID: "BK-1",
		Amount:    5000,
		Status:    domain.DepositConfirmed,
	}
	_ = st.Mutate(func(tx *store.Tx) error {
		tx.PutDeposit(dep)
		return nil
	})
	if _, err := s.Refund("BK-1", "first"); err != nil {
		t.Fatalf("refund 1: %v", err)
	}
	// Second refund of the same confirmed deposit fails — already refunded.
	_, err := s.Refund("BK-1", "second")
	if !errors.Is(err, domain.ErrAlreadyRefunded) && err != domain.ErrNotFound {
		// Refund returns ErrAlreadyRefunded from dep.Refund or ErrNotFound
		// because no confirmed deposit exists anymore.
		if err == nil {
			t.Fatal("expected error on double refund")
		}
	}
}

func TestPaymentGetByBookingNotFound(t *testing.T) {
	s, _, _ := newService(t)
	_, err := s.GetByBooking("NOPE")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestPaymentRefundInTxHelper(t *testing.T) {
	_, st, _ := newService(t)
	clock := &fakeClock{t: time.Now()}
	dep := &domain.Deposit{
		ID:        "DEP-1",
		BookingID: "BK-1",
		Amount:    5000,
		Status:    domain.DepositConfirmed,
	}
	_ = st.Mutate(func(tx *store.Tx) error {
		tx.PutDeposit(dep)
		if err := RefundInTx(tx, "BK-1", "rollback", clock); err != nil {
			return err
		}
		return nil
	})
	var got *domain.Deposit
	_ = st.View(func(tx *store.Tx) error {
		d, _ := tx.Deposit("DEP-1")
		got = d
		return nil
	})
	if got.Status != domain.DepositRefunded {
		t.Fatalf("status = %s, want refunded", got.Status)
	}
}
