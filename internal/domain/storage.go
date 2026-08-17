package domain

// StorageLocation is a dangerous-goods stowage slot at the Chuanshan
// terminal ("危货堆存位"). Each slot can hold a limited number of
// containers, tracked separately as tentative reservations and confirmed
// occupancies.
type StorageLocation struct {
	ID        string          `json:"id"`
	Zone      string          `json:"zone"`
	Capacity  int             `json:"capacity"`
	Reserved  map[string]bool `json:"reserved"`
	Confirmed map[string]bool `json:"confirmed"`
}

// NewStorageLocation constructs a location with initialised maps.
func NewStorageLocation(id, zone string, capacity int) *StorageLocation {
	return &StorageLocation{
		ID:        id,
		Zone:      zone,
		Capacity:  capacity,
		Reserved:  make(map[string]bool),
		Confirmed: make(map[string]bool),
	}
}

// UsedSlots returns the total of reserved plus confirmed slots.
func (s *StorageLocation) UsedSlots() int {
	return len(s.Reserved) + len(s.Confirmed)
}

// CanReserve reports whether a slot is available for a new reservation.
func (s *StorageLocation) CanReserve() bool {
	return s.UsedSlots() < s.Capacity
}

// Reserve tentatively books a slot for a booking.
func (s *StorageLocation) Reserve(bookingID string) error {
	if s.Reserved[bookingID] || s.Confirmed[bookingID] {
		return ErrAlreadyReserved
	}
	if !s.CanReserve() {
		return ErrNoCapacity
	}
	s.Reserved[bookingID] = true
	return nil
}

// Confirm promotes a reservation to a confirmed occupancy.
func (s *StorageLocation) Confirm(bookingID string) {
	if s.Reserved[bookingID] {
		delete(s.Reserved, bookingID)
		s.Confirmed[bookingID] = true
	}
}

// Release frees both reservation and confirmation for a booking.
func (s *StorageLocation) Release(bookingID string) {
	delete(s.Reserved, bookingID)
	delete(s.Confirmed, bookingID)
}

// AvailableSlots returns the number of free slots.
func (s *StorageLocation) AvailableSlots() int {
	return s.Capacity - s.UsedSlots()
}
