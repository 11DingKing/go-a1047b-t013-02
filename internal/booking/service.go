package booking

import (
	"errors"
	"fmt"
	"time"

	"arcticexpress/internal/domain"
	"arcticexpress/internal/payment"
	"arcticexpress/internal/store"
)

// Config holds the tunable business-rule durations.
type Config struct {
	HoldDuration     time.Duration // deposit deadline for a tentative hold
	CutoffThreshold  time.Duration // DG cert + storage must be ready before this
	PortChangeWindow time.Duration // latest time to change destination port
}

// DefaultConfig returns the production business-rule durations.
func DefaultConfig() Config {
	return Config{
		HoldDuration:     2 * time.Hour,
		CutoffThreshold:  48 * time.Hour,
		PortChangeWindow: 72 * time.Hour,
	}
}

// Service orchestrates the full booking lifecycle.
type Service struct {
	store *store.Store
	clock domain.Clock
	cfg   Config
}

// New creates a booking service.
func New(s *store.Store, clock domain.Clock, cfg Config) *Service {
	return &Service{store: s, clock: clock, cfg: cfg}
}

// CreateBookingRequest holds the cargo-owner initiated booking parameters.
type CreateBookingRequest struct {
	IdempotencyKey string
	CargoOwnerID   string
	VoyageID       string
	ContainerID    string
	Cargo          domain.Cargo
}

// Create lets a cargo owner open a booking. The idempotency key deduplicates
// retries. A container may only belong to one active booking.
func (s *Service) Create(req CreateBookingRequest) (*domain.Booking, error) {
	if req.CargoOwnerID == "" {
		return nil, errors.New("cargo owner id is required")
	}
	if req.ContainerID == "" {
		return nil, errors.New("container id is required")
	}
	if req.Cargo.Type == "" {
		return nil, errors.New("cargo type is required")
	}
	var result *domain.Booking
	err := s.store.Mutate(func(tx *store.Tx) error {
		if req.IdempotencyKey != "" {
			for _, b := range tx.AllBookings() {
				if b.IdempotencyKey == req.IdempotencyKey {
					result = b
					return nil
				}
			}
		}
		v, ok := tx.Voyage(req.VoyageID)
		if !ok {
			return fmt.Errorf("voyage %s: %w", req.VoyageID, domain.ErrNotFound)
		}
		if !v.Serves(req.Cargo.Destination) {
			return fmt.Errorf("voyage does not serve port %s: %w", req.Cargo.Destination, domain.ErrConflict)
		}
		for _, b := range tx.AllBookings() {
			if b.ContainerID == req.ContainerID && b.IsActive() {
				return fmt.Errorf("container %s already bound to active booking %s: %w", req.ContainerID, b.ID, domain.ErrConflict)
			}
		}
		now := s.clock.Now()
		b := &domain.Booking{
			ID:             tx.NextID("BK"),
			IdempotencyKey: req.IdempotencyKey,
			CargoOwnerID:   req.CargoOwnerID,
			VoyageID:       req.VoyageID,
			ContainerID:    req.ContainerID,
			Cargo:          req.Cargo,
			Destination:    req.Cargo.Destination,
			State:          domain.StateInitiated,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		tx.PutBooking(b)
		result = b
		return nil
	})
	return result, err
}

// SubmitDocumentsRequest carries the forwarder-supplied DG certificate and
// packing list.
type SubmitDocumentsRequest struct {
	BookingID   string
	ForwarderID string
	DGCert      *domain.DangerousGoodsCertificate
	PackingList *domain.PackingList
}

// SubmitDocuments lets a forwarder attach the DG certificate ("危包证") and
// packing list ("装箱清单"). Dangerous cargo must include a certificate.
func (s *Service) SubmitDocuments(req SubmitDocumentsRequest) (*domain.Booking, error) {
	if req.ForwarderID == "" {
		return nil, errors.New("forwarder id is required")
	}
	if req.PackingList == nil {
		return nil, fmt.Errorf("packing list required: %w", domain.ErrMissingDocs)
	}
	var result *domain.Booking
	err := s.store.Mutate(func(tx *store.Tx) error {
		b, ok := tx.Booking(req.BookingID)
		if !ok {
			return fmt.Errorf("booking %s: %w", req.BookingID, domain.ErrNotFound)
		}
		if b.IsDangerous() && req.DGCert == nil {
			return fmt.Errorf("dangerous cargo requires DG certificate: %w", domain.ErrMissingDocs)
		}
		if b.IsDangerous() && req.DGCert != nil {
			if req.DGCert.UNNumber == "" {
				return fmt.Errorf("DG certificate missing UN number: %w", domain.ErrMissingDocs)
			}
		}
		now := s.clock.Now()
		if err := b.Transition(domain.StateDocsSubmitted, now, "documents submitted", req.ForwarderID); err != nil {
			return err
		}
		b.ForwarderID = req.ForwarderID
		if req.DGCert != nil {
			if req.DGCert.ID == "" {
				req.DGCert.ID = tx.NextID("DGC")
			}
			b.DGCertificate = req.DGCert
		}
		b.PackingList = req.PackingList
		tx.PutBooking(b)
		result = b
		return nil
	})
	return result, err
}

// VerifyStorage lets the terminal reserve a DG storage slot for the booking.
// General cargo skips storage and moves straight to the verified state.
func (s *Service) VerifyStorage(bookingID, storageLocationID string) (*domain.Booking, error) {
	var result *domain.Booking
	err := s.store.Mutate(func(tx *store.Tx) error {
		b, ok := tx.Booking(bookingID)
		if !ok {
			return fmt.Errorf("booking %s: %w", bookingID, domain.ErrNotFound)
		}
		if b.State != domain.StateDocsSubmitted {
			return fmt.Errorf("booking in state %s: %w", b.State, domain.ErrInvalidState)
		}
		now := s.clock.Now()
		if !b.IsDangerous() {
			return b.Transition(domain.StateStorageVerified, now, "general cargo – storage not required", "terminal")
		}
		sl, ok := tx.Storage(storageLocationID)
		if !ok {
			return fmt.Errorf("storage %s: %w", storageLocationID, domain.ErrNotFound)
		}
		if err := sl.Reserve(b.ID); err != nil {
			return err
		}
		tx.PutStorage(sl)
		b.StorageLocationID = storageLocationID
		b.StorageLocked = true
		if err := b.Transition(domain.StateStorageVerified, now, "storage reserved", "terminal"); err != nil {
			return err
		}
		tx.PutBooking(b)
		result = b
		return nil
	})
	return result, err
}

// AllocateSpace lets the shipping company create a tentative hold. When no
// confirmed capacity remains the booking is placed on the waitlist.
func (s *Service) AllocateSpace(bookingID string) (*domain.Booking, error) {
	var result *domain.Booking
	err := s.store.Mutate(func(tx *store.Tx) error {
		b, ok := tx.Booking(bookingID)
		if !ok {
			return fmt.Errorf("booking %s: %w", bookingID, domain.ErrNotFound)
		}
		if b.State != domain.StateStorageVerified {
			return fmt.Errorf("booking in state %s: %w", b.State, domain.ErrInvalidState)
		}
		v, ok := tx.Voyage(b.VoyageID)
		if !ok {
			return fmt.Errorf("voyage %s: %w", b.VoyageID, domain.ErrNotFound)
		}
		now := s.clock.Now()
		if err := v.AllocateHold(b.ID, b.Cargo, now, s.cfg.HoldDuration); err != nil {
			if errors.Is(err, domain.ErrNoCapacity) {
				v.AddToWaitlist(b.ID)
				tx.PutVoyage(v)
				if err := b.Transition(domain.StateWaitlisted, now, "no confirmed capacity – waitlisted", "shipping"); err != nil {
					return err
				}
				tx.PutBooking(b)
				result = b
				return nil
			}
			return err
		}
		tx.PutVoyage(v)
		if err := b.Transition(domain.StateSpaceAllocated, now, "space allocated", "shipping"); err != nil {
			return err
		}
		tx.PutBooking(b)
		result = b
		return nil
	})
	return result, err
}

// PayDeposit records the deposit and confirms the hold. When capacity has
// already been saturated by a competing deposit the booking is placed on the
// waitlist and the deposit is immediately refunded.
func (s *Service) PayDeposit(bookingID string, amount float64, idempotencyKey string) (*domain.Booking, *domain.Deposit, error) {
	var booking *domain.Booking
	var deposit *domain.Deposit
	err := s.store.Mutate(func(tx *store.Tx) error {
		if idempotencyKey != "" {
			if existing, ok := tx.DepositByIdempotencyKey(idempotencyKey); ok {
				deposit = existing
				if b, ok := tx.Booking(existing.BookingID); ok {
					booking = b
				}
				return nil
			}
		}
		b, ok := tx.Booking(bookingID)
		if !ok {
			return fmt.Errorf("booking %s: %w", bookingID, domain.ErrNotFound)
		}
		if b.State != domain.StateSpaceAllocated {
			return fmt.Errorf("booking in state %s: %w", b.State, domain.ErrInvalidState)
		}
		v, ok := tx.Voyage(b.VoyageID)
		if !ok {
			return fmt.Errorf("voyage %s: %w", b.VoyageID, domain.ErrNotFound)
		}
		now := s.clock.Now()
		if err := v.ConfirmSpace(bookingID, now); err != nil {
			if errors.Is(err, domain.ErrNoCapacity) {
				v.ReleaseSpace(bookingID)
				v.AddToWaitlist(bookingID)
				tx.PutVoyage(v)
				if transErr := b.Transition(domain.StateWaitlisted, now, "deposit race lost", "system"); transErr != nil {
					return transErr
				}
				tx.PutBooking(b)
				dep := &domain.Deposit{
					ID:             tx.NextID("DEP"),
					BookingID:      bookingID,
					Amount:         amount,
					Currency:       "CNY",
					Status:         domain.DepositRefunded,
					IdempotencyKey: idempotencyKey,
					PaidAt:         now,
					RefundedAt:     now,
					RefundReason:   "space unavailable – waitlisted",
				}
				tx.PutDeposit(dep)
				deposit = dep
				booking = b
				return nil
			}
			return err
		}
		tx.PutVoyage(v)
		if b.StorageLocationID != "" {
			if sl, ok := tx.Storage(b.StorageLocationID); ok {
				sl.Confirm(bookingID)
				tx.PutStorage(sl)
			}
		}
		if err := b.Transition(domain.StateDepositPaid, now, "deposit paid", "system"); err != nil {
			return err
		}
		tx.PutBooking(b)
		dep := &domain.Deposit{
			ID:             tx.NextID("DEP"),
			BookingID:      bookingID,
			Amount:         amount,
			Currency:       "CNY",
			Status:         domain.DepositConfirmed,
			IdempotencyKey: idempotencyKey,
			PaidAt:         now,
		}
		tx.PutDeposit(dep)
		deposit = dep
		booking = b
		return nil
	})
	return booking, deposit, err
}

// CustomsRelease performs export inspection. Dangerous cargo must satisfy the
// cutoff compliance rule and carry a valid DG certificate.
func (s *Service) CustomsRelease(bookingID string) (*domain.Booking, error) {
	var result *domain.Booking
	err := s.store.Mutate(func(tx *store.Tx) error {
		b, ok := tx.Booking(bookingID)
		if !ok {
			return fmt.Errorf("booking %s: %w", bookingID, domain.ErrNotFound)
		}
		if b.State != domain.StateDepositPaid {
			return fmt.Errorf("booking in state %s: %w", b.State, domain.ErrInvalidState)
		}
		v, ok := tx.Voyage(b.VoyageID)
		if !ok {
			return fmt.Errorf("voyage %s: %w", b.VoyageID, domain.ErrNotFound)
		}
		now := s.clock.Now()
		if err := b.CutoffCompliant(now, v.CutoffAt, s.cfg.CutoffThreshold); err != nil {
			return err
		}
		if b.IsDangerous() && !b.DGCertificate.IsValidAt(now) {
			return fmt.Errorf("DG certificate not valid: %w", domain.ErrMissingDocs)
		}
		if !b.HasPackingList() {
			return fmt.Errorf("packing list required: %w", domain.ErrMissingDocs)
		}
		if err := b.Transition(domain.StateCustomsReleased, now, "customs released", "customs"); err != nil {
			return err
		}
		tx.PutBooking(b)
		result = b
		return nil
	})
	return result, err
}

// CustomsReject triggers a rollback: space and storage are released, the
// deposit is refunded, and the booking enters pending review while the DG
// certificate and packing list are retained for re-queueing.
func (s *Service) CustomsReject(bookingID, reason string) (*domain.Booking, error) {
	var result *domain.Booking
	err := s.store.Mutate(func(tx *store.Tx) error {
		b, ok := tx.Booking(bookingID)
		if !ok {
			return fmt.Errorf("booking %s: %w", bookingID, domain.ErrNotFound)
		}
		if b.State != domain.StateCustomsReleased && b.State != domain.StateDepositPaid {
			return fmt.Errorf("booking in state %s: %w", b.State, domain.ErrInvalidState)
		}
		now := s.clock.Now()
		if b.State == domain.StateDepositPaid {
			if err := b.Transition(domain.StateCustomsReleased, now, "customs review", "customs"); err != nil {
				return err
			}
		}
		if err := b.Transition(domain.StateRejected, now, reason, "customs"); err != nil {
			return err
		}
		s.rollback(tx, b, reason, now)
		if err := b.Transition(domain.StatePendingReview, now, reason, "customs"); err != nil {
			return err
		}
		tx.PutBooking(b)
		result = b
		return nil
	})
	return result, err
}

// BindBill links the bill of lading and consignee, enforcing the "one
// container, one B/L, one consignee" rule.
func (s *Service) BindBill(bookingID, billOfLadingID, consigneeID string) (*domain.Booking, error) {
	if billOfLadingID == "" || consigneeID == "" {
		return nil, errors.New("bill of lading and consignee are required")
	}
	var result *domain.Booking
	err := s.store.Mutate(func(tx *store.Tx) error {
		b, ok := tx.Booking(bookingID)
		if !ok {
			return fmt.Errorf("booking %s: %w", bookingID, domain.ErrNotFound)
		}
		for _, other := range tx.AllBookings() {
			if other.ID == bookingID || !other.IsActive() {
				continue
			}
			if other.ContainerID == b.ContainerID && other.BillOfLadingID != "" {
				return fmt.Errorf("container %s already has B/L %s: %w", b.ContainerID, other.BillOfLadingID, domain.ErrConflict)
			}
			if billOfLadingID != "" && other.BillOfLadingID == billOfLadingID {
				return fmt.Errorf("bill of lading %s already bound to booking %s: %w", billOfLadingID, other.ID, domain.ErrConflict)
			}
		}
		b.BillOfLadingID = billOfLadingID
		b.ConsigneeID = consigneeID
		b.UpdatedAt = s.clock.Now()
		tx.PutBooking(b)
		result = b
		return nil
	})
	return result, err
}

// Confirm finalises a customs-released booking once the B/L is bound.
func (s *Service) Confirm(bookingID string) (*domain.Booking, error) {
	var result *domain.Booking
	err := s.store.Mutate(func(tx *store.Tx) error {
		b, ok := tx.Booking(bookingID)
		if !ok {
			return fmt.Errorf("booking %s: %w", bookingID, domain.ErrNotFound)
		}
		if b.State != domain.StateCustomsReleased {
			return fmt.Errorf("booking in state %s: %w", b.State, domain.ErrInvalidState)
		}
		if b.BillOfLadingID == "" || b.ConsigneeID == "" {
			return fmt.Errorf("bill of lading and consignee must be bound: %w", domain.ErrMissingDocs)
		}
		if err := b.Transition(domain.StateConfirmed, s.clock.Now(), "booking confirmed", "shipping"); err != nil {
			return err
		}
		tx.PutBooking(b)
		result = b
		return nil
	})
	return result, err
}

// Cancel rolls back resources and marks the booking cancelled.
func (s *Service) Cancel(bookingID, reason string) (*domain.Booking, error) {
	var result *domain.Booking
	err := s.store.Mutate(func(tx *store.Tx) error {
		b, ok := tx.Booking(bookingID)
		if !ok {
			return fmt.Errorf("booking %s: %w", bookingID, domain.ErrNotFound)
		}
		if b.State == domain.StateCancelled || b.State == domain.StatePendingReview {
			result = b
			return nil
		}
		now := s.clock.Now()
		s.rollback(tx, b, reason, now)
		if err := b.Transition(domain.StateCancelled, now, reason, "system"); err != nil {
			return err
		}
		b.CancelReason = reason
		tx.PutBooking(b)
		result = b
		return nil
	})
	return result, err
}

// ChangePort updates the destination, requiring written confirmation within
// the port-change window before departure.
func (s *Service) ChangePort(bookingID string, newPort domain.DestinationPort) (*domain.Booking, error) {
	var result *domain.Booking
	err := s.store.Mutate(func(tx *store.Tx) error {
		b, ok := tx.Booking(bookingID)
		if !ok {
			return fmt.Errorf("booking %s: %w", bookingID, domain.ErrNotFound)
		}
		v, ok := tx.Voyage(b.VoyageID)
		if !ok {
			return fmt.Errorf("voyage %s: %w", b.VoyageID, domain.ErrNotFound)
		}
		now := s.clock.Now()
		if !v.CanChangePort(now, s.cfg.PortChangeWindow) {
			return fmt.Errorf("port change window closed (need %s before departure): %w", s.cfg.PortChangeWindow, domain.ErrWindowClosed)
		}
		if !v.Serves(newPort) {
			return fmt.Errorf("voyage does not serve %s: %w", newPort, domain.ErrConflict)
		}
		b.Cargo.Destination = newPort
		b.Destination = newPort
		b.UpdatedAt = now
		tx.PutBooking(b)
		result = b
		return nil
	})
	return result, err
}

// Requeue lets a specialist re-activate a pending-review booking. The DG
// certificate and packing list are retained so the booking re-enters the
// flow at the docs-submitted state.
func (s *Service) Requeue(bookingID string) (*domain.Booking, error) {
	var result *domain.Booking
	err := s.store.Mutate(func(tx *store.Tx) error {
		b, ok := tx.Booking(bookingID)
		if !ok {
			return fmt.Errorf("booking %s: %w", bookingID, domain.ErrNotFound)
		}
		if b.State != domain.StatePendingReview {
			return fmt.Errorf("booking in state %s: %w", b.State, domain.ErrInvalidState)
		}
		now := s.clock.Now()
		b.RequeueCount++
		if err := b.Transition(domain.StateInitiated, now, "re-queued by specialist", "specialist"); err != nil {
			return err
		}
		if b.HasDGCertificate() && b.HasPackingList() {
			if err := b.Transition(domain.StateDocsSubmitted, now, "documents retained", "specialist"); err != nil {
				return err
			}
		}
		tx.PutBooking(b)
		result = b
		return nil
	})
	return result, err
}

// Get retrieves a booking by ID.
func (s *Service) Get(bookingID string) (*domain.Booking, error) {
	var result *domain.Booking
	err := s.store.View(func(tx *store.Tx) error {
		b, ok := tx.Booking(bookingID)
		if !ok {
			return fmt.Errorf("booking %s: %w", bookingID, domain.ErrNotFound)
		}
		result = b
		return nil
	})
	return result, err
}

// List returns all bookings.
func (s *Service) List() ([]*domain.Booking, error) {
	var result []*domain.Booking
	err := s.store.View(func(tx *store.Tx) error {
		result = tx.AllBookings()
		return nil
	})
	return result, err
}

// CancelExpiredHold is invoked by the scheduler for holds whose deposit
// deadline has passed. Only bookings still in the space_allocated state are
// affected.
func (s *Service) CancelExpiredHold(bookingID string) error {
	return s.store.Mutate(func(tx *store.Tx) error {
		b, ok := tx.Booking(bookingID)
		if !ok {
			return nil
		}
		if b.State != domain.StateSpaceAllocated {
			return nil
		}
		now := s.clock.Now()
		s.rollback(tx, b, "deposit timeout", now)
		if err := b.Transition(domain.StateCancelled, now, "deposit timeout – hold expired", "scheduler"); err != nil {
			return err
		}
		b.CancelReason = "deposit timeout"
		tx.PutBooking(b)
		return nil
	})
}

// CancelForCutoffNonCompliance is invoked by the scheduler for dangerous
// bookings that breach the cutoff threshold without a certificate or storage.
func (s *Service) CancelForCutoffNonCompliance(bookingID, reason string) error {
	return s.store.Mutate(func(tx *store.Tx) error {
		b, ok := tx.Booking(bookingID)
		if !ok {
			return nil
		}
		if b.State != domain.StateDepositPaid && b.State != domain.StateCustomsReleased {
			return nil
		}
		now := s.clock.Now()
		s.rollback(tx, b, reason, now)
		if err := b.Transition(domain.StateCancelled, now, reason, "scheduler"); err != nil {
			return err
		}
		b.CancelReason = reason
		tx.PutBooking(b)
		return nil
	})
}

// rollback releases voyage space, storage and the confirmed deposit, then
// promotes the first eligible waitlisted booking. The DG certificate and
// packing list are deliberately retained.
func (s *Service) rollback(tx *store.Tx, b *domain.Booking, reason string, now time.Time) {
	if v, ok := tx.Voyage(b.VoyageID); ok {
		v.ReleaseSpace(b.ID)
		s.promoteWaitlist(tx, v, now)
		tx.PutVoyage(v)
	}
	if b.StorageLocationID != "" {
		if sl, ok := tx.Storage(b.StorageLocationID); ok {
			sl.Release(b.ID)
			tx.PutStorage(sl)
		}
		b.StorageLocked = false
	}
	_ = payment.RefundInTx(tx, b.ID, reason, s.clock)
}

// promoteWaitlist moves waitlisted bookings into tentative holds while
// capacity is available.
func (s *Service) promoteWaitlist(tx *store.Tx, v *domain.Voyage, now time.Time) {
	for {
		nextID := v.NextWaitlisted()
		if nextID == "" {
			return
		}
		wb, ok := tx.Booking(nextID)
		if !ok || wb.State != domain.StateWaitlisted {
			v.RemoveFromWaitlist(nextID)
			continue
		}
		if !v.CanAllocateHold(wb.Cargo.Type) {
			return
		}
		v.RemoveFromWaitlist(nextID)
		if err := v.AllocateHold(nextID, wb.Cargo, now, s.cfg.HoldDuration); err != nil {
			v.AddToWaitlist(nextID)
			return
		}
		if err := wb.Transition(domain.StateSpaceAllocated, now, "promoted from waitlist", "scheduler"); err != nil {
			v.AddToWaitlist(nextID)
			return
		}
		tx.PutBooking(wb)
	}
}
