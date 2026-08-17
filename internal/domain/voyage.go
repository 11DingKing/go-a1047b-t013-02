package domain

import (
	"fmt"
	"time"
)

// HoldAllocation tracks a tentative or confirmed space reservation on a voyage.
type HoldAllocation struct {
	BookingID   string          `json:"booking_id"`
	CargoType   CargoType       `json:"cargo_type"`
	Destination DestinationPort `json:"destination"`
	ReservedAt  time.Time       `json:"reserved_at"`
	ExpiresAt   time.Time       `json:"expires_at"`
	ConfirmedAt time.Time       `json:"confirmed_at,omitempty"`
}

// Voyage represents a single Arctic Express sailing from Chuanshan to Europe.
// Dangerous and general cargo use independent capacity pools so that the
// "dangerous and general cargo must not share a hold" rule is enforced by
// construction.
type Voyage struct {
	ID           string                    `json:"id"`
	VesselName   string                    `json:"vessel_name"`
	VoyageNumber string                    `json:"voyage_number"`
	DepartureAt  time.Time                 `json:"departure_at"`
	CutoffAt     time.Time                 `json:"cutoff_at"`
	Destinations []DestinationPort         `json:"destinations"`
	DGCapacity   int                       `json:"dg_capacity"`
	GenCapacity  int                       `json:"gen_capacity"`
	Holds        map[string]HoldAllocation `json:"holds"`
	Confirmed    map[string]HoldAllocation `json:"confirmed"`
	Waitlist     []string                  `json:"waitlist"`
}

// NewVoyage constructs a Voyage with initialised allocation maps.
func NewVoyage(id, vessel, number string, departure, cutoff time.Time, dests []DestinationPort, dgCap, genCap int) *Voyage {
	return &Voyage{
		ID:           id,
		VesselName:   vessel,
		VoyageNumber: number,
		DepartureAt:  departure,
		CutoffAt:     cutoff,
		Destinations: dests,
		DGCapacity:   dgCap,
		GenCapacity:  genCap,
		Holds:        make(map[string]HoldAllocation),
		Confirmed:    make(map[string]HoldAllocation),
	}
}

// Serves reports whether the voyage calls at the given destination port.
func (v *Voyage) Serves(dest DestinationPort) bool {
	for _, d := range v.Destinations {
		if d == dest {
			return true
		}
	}
	return false
}

func (v *Voyage) capacityFor(ct CargoType) int {
	if ct.IsDangerous() {
		return v.DGCapacity
	}
	return v.GenCapacity
}

func (v *Voyage) confirmedCountFor(ct CargoType) int {
	n := 0
	isDG := ct.IsDangerous()
	for _, h := range v.Confirmed {
		if h.CargoType.IsDangerous() == isDG {
			n++
		}
	}
	return n
}

// HoldCountFor returns the number of tentative holds for a cargo type.
func (v *Voyage) HoldCountFor(ct CargoType) int {
	n := 0
	isDG := ct.IsDangerous()
	for _, h := range v.Holds {
		if h.CargoType.IsDangerous() == isDG {
			n++
		}
	}
	return n
}

// AvailableFor returns the remaining confirmed capacity for a cargo type.
func (v *Voyage) AvailableFor(ct CargoType) int {
	return v.capacityFor(ct) - v.confirmedCountFor(ct)
}

// CanAllocateHold reports whether a new tentative hold may be created.
// Holds are allowed while confirmed bookings have not saturated capacity,
// which permits multiple forwarders to hold the same remaining slot and lets
// deposit arrival decide the winner.
func (v *Voyage) CanAllocateHold(ct CargoType) bool {
	return v.confirmedCountFor(ct) < v.capacityFor(ct)
}

// AllocateHold creates a tentative reservation with a deposit deadline.
func (v *Voyage) AllocateHold(bookingID string, cargo Cargo, now time.Time, holdDuration time.Duration) error {
	if _, ok := v.Holds[bookingID]; ok {
		return ErrAlreadyAllocated
	}
	if _, ok := v.Confirmed[bookingID]; ok {
		return ErrAlreadyConfirmed
	}
	if !v.CanAllocateHold(cargo.Type) {
		return fmt.Errorf("voyage %s has no %s capacity left: %v", v.ID, cargo.Type, ErrNoCapacity)
	}
	v.Holds[bookingID] = HoldAllocation{
		BookingID:   bookingID,
		CargoType:   cargo.Type,
		Destination: cargo.Destination,
		ReservedAt:  now,
		ExpiresAt:   now.Add(holdDuration),
	}
	return nil
}

// ConfirmSpace converts a tentative hold into a confirmed booking. It returns
// ErrNoCapacity when the capacity has been saturated by an earlier deposit,
// allowing the caller to place the loser on the waitlist.
func (v *Voyage) ConfirmSpace(bookingID string, now time.Time) error {
	h, ok := v.Holds[bookingID]
	if !ok {
		if _, ok := v.Confirmed[bookingID]; ok {
			return ErrAlreadyConfirmed
		}
		return ErrNoHold
	}
	if v.confirmedCountFor(h.CargoType) >= v.capacityFor(h.CargoType) {
		return ErrNoCapacity
	}
	h.ConfirmedAt = now
	delete(v.Holds, bookingID)
	v.Confirmed[bookingID] = h
	return nil
}

// ReleaseSpace removes both tentative and confirmed allocations for a booking.
func (v *Voyage) ReleaseSpace(bookingID string) {
	delete(v.Holds, bookingID)
	delete(v.Confirmed, bookingID)
}

// HasAllocation reports whether the booking holds or has confirmed space.
func (v *Voyage) HasAllocation(bookingID string) bool {
	_, h := v.Holds[bookingID]
	_, c := v.Confirmed[bookingID]
	return h || c
}

// IsConfirmed reports whether the booking has paid its deposit.
func (v *Voyage) IsConfirmed(bookingID string) bool {
	_, ok := v.Confirmed[bookingID]
	return ok
}

// AddToWaitlist appends a booking to the end of the waitlist unless present.
func (v *Voyage) AddToWaitlist(bookingID string) {
	for _, id := range v.Waitlist {
		if id == bookingID {
			return
		}
	}
	v.Waitlist = append(v.Waitlist, bookingID)
}

// RemoveFromWaitlist removes a booking from the waitlist.
func (v *Voyage) RemoveFromWaitlist(bookingID string) {
	for i, id := range v.Waitlist {
		if id == bookingID {
			v.Waitlist = append(v.Waitlist[:i], v.Waitlist[i+1:]...)
			return
		}
	}
}

// NextWaitlisted returns the first booking on the waitlist, or "".
func (v *Voyage) NextWaitlisted() string {
	if len(v.Waitlist) == 0 {
		return ""
	}
	return v.Waitlist[0]
}

// ExpiredHolds returns booking IDs whose tentative hold has passed its
// deposit deadline.
func (v *Voyage) ExpiredHolds(now time.Time) []string {
	var expired []string
	for id, h := range v.Holds {
		if !now.Before(h.ExpiresAt) {
			expired = append(expired, id)
		}
	}
	return expired
}

// CanChangePort reports whether a destination change is still permitted.
func (v *Voyage) CanChangePort(now time.Time, window time.Duration) bool {
	return v.DepartureAt.Sub(now) >= window
}
