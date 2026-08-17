package voyage

import (
	"errors"
	"fmt"
	"time"

	"arcticexpress/internal/domain"
	"arcticexpress/internal/store"
)

// Service manages voyage lifecycle and capacity queries.
type Service struct {
	store *store.Store
	clock domain.Clock
}

// New creates a voyage service.
func New(s *store.Store, clock domain.Clock) *Service {
	return &Service{store: s, clock: clock}
}

// CreateVoyageRequest holds the parameters for creating a new sailing.
type CreateVoyageRequest struct {
	VesselName   string
	VoyageNumber string
	DepartureAt  time.Time
	CutoffAt     time.Time
	Destinations []domain.DestinationPort
	DGCapacity   int
	GenCapacity  int
}

// Create validates input and persists a new voyage.
func (s *Service) Create(req CreateVoyageRequest) (*domain.Voyage, error) {
	if req.VoyageNumber == "" {
		return nil, errors.New("voyage number is required")
	}
	if req.DepartureAt.IsZero() {
		return nil, errors.New("departure time is required")
	}
	if !req.CutoffAt.Before(req.DepartureAt) {
		return nil, errors.New("cutoff must be before departure")
	}
	if len(req.Destinations) == 0 {
		return nil, errors.New("at least one destination port is required")
	}
	if req.DGCapacity < 0 || req.GenCapacity < 0 {
		return nil, errors.New("capacity must be non-negative")
	}
	var result *domain.Voyage
	err := s.store.Mutate(func(tx *store.Tx) error {
		v := domain.NewVoyage(
			tx.NextID("VY"),
			req.VesselName,
			req.VoyageNumber,
			req.DepartureAt,
			req.CutoffAt,
			req.Destinations,
			req.DGCapacity,
			req.GenCapacity,
		)
		tx.PutVoyage(v)
		result = v
		return nil
	})
	return result, err
}

// Get retrieves a voyage by ID.
func (s *Service) Get(id string) (*domain.Voyage, error) {
	var result *domain.Voyage
	err := s.store.View(func(tx *store.Tx) error {
		v, ok := tx.Voyage(id)
		if !ok {
			return fmt.Errorf("voyage %s: %w", id, domain.ErrNotFound)
		}
		result = v
		return nil
	})
	return result, err
}

// List returns all voyages.
func (s *Service) List() ([]*domain.Voyage, error) {
	var result []*domain.Voyage
	err := s.store.View(func(tx *store.Tx) error {
		result = tx.AllVoyages()
		return nil
	})
	return result, err
}

// CapacityReport summarises remaining space for a voyage.
type CapacityReport struct {
	VoyageID       string                   `json:"voyage_id"`
	VoyageNumber   string                   `json:"voyage_number"`
	AvailableDG    int                      `json:"available_dg"`
	AvailableGen   int                      `json:"available_gen"`
	HoldsDG        int                      `json:"holds_dg"`
	HoldsGen       int                      `json:"holds_gen"`
	WaitlistLength int                      `json:"waitlist_length"`
	Destinations   []domain.DestinationPort `json:"destinations"`
	DepartureAt    time.Time                `json:"departure_at"`
}

// Capacity returns a capacity report for the given voyage.
func (s *Service) Capacity(id string) (*CapacityReport, error) {
	var report *CapacityReport
	err := s.store.View(func(tx *store.Tx) error {
		v, ok := tx.Voyage(id)
		if !ok {
			return fmt.Errorf("voyage %s: %w", id, domain.ErrNotFound)
		}
		report = &CapacityReport{
			VoyageID:       v.ID,
			VoyageNumber:   v.VoyageNumber,
			AvailableDG:    v.AvailableFor(domain.CargoBattery),
			AvailableGen:   v.AvailableFor(domain.CargoGeneral),
			HoldsDG:        v.HoldCountFor(domain.CargoBattery),
			HoldsGen:       v.HoldCountFor(domain.CargoGeneral),
			WaitlistLength: len(v.Waitlist),
			Destinations:   v.Destinations,
			DepartureAt:    v.DepartureAt,
		}
		return nil
	})
	return report, err
}
