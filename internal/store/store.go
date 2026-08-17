package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"arcticexpress/internal/domain"
)

// Data is the persisted aggregate root containing every entity.
type Data struct {
	Bookings map[string]*domain.Booking         `json:"bookings"`
	Voyages  map[string]*domain.Voyage          `json:"voyages"`
	Storages map[string]*domain.StorageLocation `json:"storages"`
	Deposits map[string]*domain.Deposit         `json:"deposits"`
	Counter  int64                              `json:"counter"`
}

func newData() *Data {
	return &Data{
		Bookings: make(map[string]*domain.Booking),
		Voyages:  make(map[string]*domain.Voyage),
		Storages: make(map[string]*domain.StorageLocation),
		Deposits: make(map[string]*domain.Deposit),
	}
}

// clone produces a deep copy of the data via JSON round-trip so that
// mutations inside a transaction can be discarded on error without corrupting
// the live in-memory state.
func (d *Data) clone() *Data {
	b, err := json.Marshal(d)
	if err != nil {
		panic(fmt.Sprintf("store: marshal for clone: %v", err))
	}
	c := newData()
	if err := json.Unmarshal(b, c); err != nil {
		panic(fmt.Sprintf("store: unmarshal for clone: %v", err))
	}
	c.ensureMaps()
	return c
}

func (d *Data) ensureMaps() {
	if d.Bookings == nil {
		d.Bookings = make(map[string]*domain.Booking)
	}
	if d.Voyages == nil {
		d.Voyages = make(map[string]*domain.Voyage)
	}
	if d.Storages == nil {
		d.Storages = make(map[string]*domain.StorageLocation)
	}
	if d.Deposits == nil {
		d.Deposits = make(map[string]*domain.Deposit)
	}
	for _, v := range d.Voyages {
		if v.Holds == nil {
			v.Holds = make(map[string]domain.HoldAllocation)
		}
		if v.Confirmed == nil {
			v.Confirmed = make(map[string]domain.HoldAllocation)
		}
	}
	for _, s := range d.Storages {
		if s.Reserved == nil {
			s.Reserved = make(map[string]bool)
		}
		if s.Confirmed == nil {
			s.Confirmed = make(map[string]bool)
		}
	}
}

// Store is a concurrency-safe, file-backed in-memory repository.
type Store struct {
	mu   sync.RWMutex
	data *Data
	path string
}

// New opens or creates a store at the given path. An empty path disables
// disk persistence (useful for tests).
func New(path string) (*Store, error) {
	s := &Store{data: newData(), path: path}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	if s.path == "" {
		return nil
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(b) == 0 {
		return nil
	}
	d := newData()
	if err := json.Unmarshal(b, d); err != nil {
		return fmt.Errorf("store: load %s: %w", s.path, err)
	}
	d.ensureMaps()
	s.data = d
	return nil
}

func (s *Store) saveData(d *Data) error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Tx is a transaction handle giving services access to entity maps within a
// locked section.
type Tx struct {
	data *Data
}

// Mutate runs fn against a private copy of the data. If fn returns nil the
// copy replaces the live data and is persisted; otherwise the copy is
// discarded, leaving the live state untouched.
func (s *Store) Mutate(fn func(*Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	working := s.data.clone()
	tx := &Tx{data: working}
	if err := fn(tx); err != nil {
		return err
	}
	if err := s.saveData(working); err != nil {
		return err
	}
	s.data = working
	return nil
}

// View runs fn against the live data under a read lock. The callback must not
// mutate the data.
func (s *Store) View(fn func(*Tx) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fn(&Tx{data: s.data})
}

// --- Read accessors ---

func (tx *Tx) Booking(id string) (*domain.Booking, bool) {
	b, ok := tx.data.Bookings[id]
	return b, ok
}

func (tx *Tx) Voyage(id string) (*domain.Voyage, bool) {
	v, ok := tx.data.Voyages[id]
	return v, ok
}

func (tx *Tx) Storage(id string) (*domain.StorageLocation, bool) {
	sl, ok := tx.data.Storages[id]
	return sl, ok
}

func (tx *Tx) Deposit(id string) (*domain.Deposit, bool) {
	d, ok := tx.data.Deposits[id]
	return d, ok
}

func (tx *Tx) ConfirmedDepositByBooking(bookingID string) (*domain.Deposit, bool) {
	for _, d := range tx.data.Deposits {
		if d.BookingID == bookingID && d.Status == domain.DepositConfirmed {
			return d, true
		}
	}
	return nil, false
}

func (tx *Tx) DepositByIdempotencyKey(key string) (*domain.Deposit, bool) {
	if key == "" {
		return nil, false
	}
	for _, d := range tx.data.Deposits {
		if d.IdempotencyKey == key {
			return d, true
		}
	}
	return nil, false
}

func (tx *Tx) AllBookings() []*domain.Booking {
	list := make([]*domain.Booking, 0, len(tx.data.Bookings))
	for _, b := range tx.data.Bookings {
		list = append(list, b)
	}
	return list
}

func (tx *Tx) AllVoyages() []*domain.Voyage {
	list := make([]*domain.Voyage, 0, len(tx.data.Voyages))
	for _, v := range tx.data.Voyages {
		list = append(list, v)
	}
	return list
}

func (tx *Tx) AllStorages() []*domain.StorageLocation {
	list := make([]*domain.StorageLocation, 0, len(tx.data.Storages))
	for _, s := range tx.data.Storages {
		list = append(list, s)
	}
	return list
}

func (tx *Tx) AllDeposits() []*domain.Deposit {
	list := make([]*domain.Deposit, 0, len(tx.data.Deposits))
	for _, d := range tx.data.Deposits {
		list = append(list, d)
	}
	return list
}

// --- Write accessors ---

func (tx *Tx) PutBooking(b *domain.Booking)          { tx.data.Bookings[b.ID] = b }
func (tx *Tx) PutVoyage(v *domain.Voyage)            { tx.data.Voyages[v.ID] = v }
func (tx *Tx) PutStorage(sl *domain.StorageLocation) { tx.data.Storages[sl.ID] = sl }
func (tx *Tx) PutDeposit(d *domain.Deposit)          { tx.data.Deposits[d.ID] = d }

// NextID produces a monotonically increasing ID with the given prefix.
func (tx *Tx) NextID(prefix string) string {
	tx.data.Counter++
	return fmt.Sprintf("%s-%d", prefix, tx.data.Counter)
}
