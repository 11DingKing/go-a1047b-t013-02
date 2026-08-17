package payment

import (
	"fmt"

	"arcticexpress/internal/domain"
	"arcticexpress/internal/store"
)

// Service provides deposit queries and standalone refund operations.
type Service struct {
	store *store.Store
	clock domain.Clock
}

// New creates a payment service.
func New(s *store.Store, clock domain.Clock) *Service {
	return &Service{store: s, clock: clock}
}

// GetByBooking returns the most relevant deposit for a booking (confirmed
// preferred, otherwise the first found).
func (s *Service) GetByBooking(bookingID string) (*domain.Deposit, error) {
	var result *domain.Deposit
	err := s.store.View(func(tx *store.Tx) error {
		if d, ok := tx.ConfirmedDepositByBooking(bookingID); ok {
			result = d
			return nil
		}
		for _, d := range tx.AllDeposits() {
			if d.BookingID == bookingID {
				result = d
				return nil
			}
		}
		return fmt.Errorf("deposit for booking %s: %w", bookingID, domain.ErrNotFound)
	})
	return result, err
}

// List returns all deposits.
func (s *Service) List() ([]*domain.Deposit, error) {
	var result []*domain.Deposit
	err := s.store.View(func(tx *store.Tx) error {
		result = tx.AllDeposits()
		return nil
	})
	return result, err
}

// Refund deposits a refund for a confirmed deposit belonging to the booking.
// This is the standalone (admin-initiated) variant; booking rollback uses the
// in-transaction helper below.
func (s *Service) Refund(bookingID, reason string) (*domain.Deposit, error) {
	var result *domain.Deposit
	err := s.store.Mutate(func(tx *store.Tx) error {
		d, ok := tx.ConfirmedDepositByBooking(bookingID)
		if !ok {
			return fmt.Errorf("no confirmed deposit for booking %s: %w", bookingID, domain.ErrNotFound)
		}
		if err := d.Refund(reason, s.clock.Now()); err != nil {
			return err
		}
		tx.PutDeposit(d)
		result = d
		return nil
	})
	return result, err
}

// RefundInTx refunds the confirmed deposit for a booking inside an existing
// transaction. It is a no-op when no confirmed deposit exists.
func RefundInTx(tx *store.Tx, bookingID, reason string, now domain.Clock) error {
	d, ok := tx.ConfirmedDepositByBooking(bookingID)
	if !ok {
		return nil
	}
	if err := d.Refund(reason, now.Now()); err != nil {
		return err
	}
	tx.PutDeposit(d)
	return nil
}
