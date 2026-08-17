package domain

import (
	"fmt"
	"time"
)

// BookingState enumerates every state a booking may occupy.
type BookingState string

const (
	StateInitiated       BookingState = "initiated"
	StateDocsSubmitted   BookingState = "docs_submitted"
	StateStorageVerified BookingState = "storage_verified"
	StateSpaceAllocated  BookingState = "space_allocated"
	StateDepositPaid     BookingState = "deposit_paid"
	StateCustomsReleased BookingState = "customs_released"
	StateConfirmed       BookingState = "confirmed"
	StateWaitlisted      BookingState = "waitlisted"
	StatePendingReview   BookingState = "pending_review"
	StateRejected        BookingState = "rejected"
	StateCancelled       BookingState = "cancelled"
)

// StateTransition records one step in the booking's history.
type StateTransition struct {
	From     BookingState `json:"from"`
	To       BookingState `json:"to"`
	At       time.Time    `json:"at"`
	Reason   string       `json:"reason"`
	Operator string       `json:"operator"`
}

// validTransitions defines the legal state-machine edges. Every active state
// can transition to cancelled; confirmed is terminal.
var validTransitions = map[BookingState][]BookingState{
	StateInitiated:       {StateDocsSubmitted, StateCancelled},
	StateDocsSubmitted:   {StateStorageVerified, StateCancelled},
	StateStorageVerified: {StateSpaceAllocated, StateWaitlisted, StateCancelled},
	StateSpaceAllocated:  {StateDepositPaid, StateWaitlisted, StateCancelled},
	StateWaitlisted:      {StateSpaceAllocated, StateCancelled},
	StateDepositPaid:     {StateCustomsReleased, StateCancelled},
	StateCustomsReleased: {StateConfirmed, StateRejected, StateCancelled},
	StateRejected:        {StatePendingReview},
	StatePendingReview:   {StateInitiated, StateCancelled},
	StateConfirmed:       {},
	StateCancelled:       {},
}

// CanTransitionTo reports whether the transition is legal.
func (b *Booking) CanTransitionTo(target BookingState) bool {
	for _, s := range validTransitions[b.State] {
		if s == target {
			return true
		}
	}
	return false
}

// Transition applies a state change, recording it in the history. It returns
// a wrapped ErrInvalidState when the edge is not legal.
func (b *Booking) Transition(target BookingState, at time.Time, reason, operator string) error {
	if !b.CanTransitionTo(target) {
		return fmt.Errorf("cannot transition %s -> %s: %w", b.State, target, ErrInvalidState)
	}
	b.History = append(b.History, StateTransition{
		From: b.State, To: target, At: at, Reason: reason, Operator: operator,
	})
	b.State = target
	b.UpdatedAt = at
	return nil
}

// Booking is the central aggregate of the Arctic Express booking domain.
type Booking struct {
	ID                string                     `json:"id"`
	IdempotencyKey    string                     `json:"idempotency_key"`
	CargoOwnerID      string                     `json:"cargo_owner_id"`
	ForwarderID       string                     `json:"forwarder_id"`
	VoyageID          string                     `json:"voyage_id"`
	ContainerID       string                     `json:"container_id"`
	Cargo             Cargo                      `json:"cargo"`
	Destination       DestinationPort            `json:"destination"`
	State             BookingState               `json:"state"`
	DGCertificate     *DangerousGoodsCertificate `json:"dg_certificate,omitempty"`
	PackingList       *PackingList               `json:"packing_list,omitempty"`
	StorageLocationID string                     `json:"storage_location_id"`
	StorageLocked     bool                       `json:"storage_locked"`
	BillOfLadingID    string                     `json:"bill_of_lading_id"`
	ConsigneeID       string                     `json:"consignee_id"`
	CreatedAt         time.Time                  `json:"created_at"`
	UpdatedAt         time.Time                  `json:"updated_at"`
	History           []StateTransition          `json:"history"`
	CancelReason      string                     `json:"cancel_reason"`
	RequeueCount      int                        `json:"requeue_count"`
}

// HasDGCertificate reports whether a DG certificate has been uploaded.
func (b *Booking) HasDGCertificate() bool { return b.DGCertificate != nil }

// HasPackingList reports whether a packing list has been uploaded.
func (b *Booking) HasPackingList() bool { return b.PackingList != nil }

// IsDangerous reports whether the cargo requires DG handling.
func (b *Booking) IsDangerous() bool { return b.Cargo.Type.IsDangerous() }

// IsActive reports whether the booking is still progressing (not terminal).
func (b *Booking) IsActive() bool {
	switch b.State {
	case StateCancelled, StateConfirmed, StatePendingReview:
		return false
	default:
		return true
	}
}

// CutoffCompliant enforces the "battery/dangerous cargo must have a DG
// certificate and locked storage 48 hours before cutoff" rule. When the
// remaining time to cutoff is within the threshold the booking must already
// have its certificate and storage locked.
func (b *Booking) CutoffCompliant(now time.Time, cutoff time.Time, threshold time.Duration) error {
	if !b.IsDangerous() {
		return nil
	}
	if cutoff.Sub(now) > threshold {
		return nil
	}
	if !b.HasDGCertificate() {
		return fmt.Errorf("dangerous cargo missing DG certificate within cutoff threshold: %w", ErrMissingDocs)
	}
	if !b.DGCertificate.IsValidAt(now) {
		return fmt.Errorf("DG certificate expired: %w", ErrMissingDocs)
	}
	if !b.StorageLocked {
		return fmt.Errorf("dangerous cargo storage not locked within cutoff threshold: %w", ErrMissingDocs)
	}
	return nil
}
