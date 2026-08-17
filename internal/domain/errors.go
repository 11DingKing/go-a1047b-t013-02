package domain

import "errors"

// Sentinel errors shared across the domain. Use errors.Is for comparison.
var (
	ErrNotFound         = errors.New("entity not found")
	ErrAlreadyAllocated = errors.New("space already allocated for booking")
	ErrAlreadyConfirmed = errors.New("space already confirmed for booking")
	ErrAlreadyReserved  = errors.New("storage already reserved for booking")
	ErrAlreadyRefunded  = errors.New("deposit already refunded")
	ErrNoCapacity       = errors.New("no capacity available")
	ErrNoHold           = errors.New("no hold found for booking")
	ErrInvalidState     = errors.New("invalid state for operation")
	ErrConflict         = errors.New("resource conflict")
	ErrMissingDocs      = errors.New("required documents missing")
	ErrWindowClosed     = errors.New("operation window has closed")
)
