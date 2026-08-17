package domain

import "time"

// DepositStatus tracks the lifecycle of a booking deposit.
type DepositStatus string

const (
	DepositConfirmed DepositStatus = "confirmed"
	DepositRefunded  DepositStatus = "refunded"
)

// Deposit records the money received for a booking and its refund state.
type Deposit struct {
	ID             string        `json:"id"`
	BookingID      string        `json:"booking_id"`
	Amount         float64       `json:"amount"`
	Currency       string        `json:"currency"`
	Status         DepositStatus `json:"status"`
	IdempotencyKey string        `json:"idempotency_key"`
	PaidAt         time.Time     `json:"paid_at"`
	RefundedAt     time.Time     `json:"refunded_at,omitempty"`
	RefundReason   string        `json:"refund_reason,omitempty"`
}

// Refund marks the deposit as refunded with the given reason.
func (d *Deposit) Refund(reason string, at time.Time) error {
	if d.Status == DepositRefunded {
		return ErrAlreadyRefunded
	}
	d.Status = DepositRefunded
	d.RefundedAt = at
	d.RefundReason = reason
	return nil
}
