package storage

import (
	"errors"
	"fmt"

	"arcticexpress/internal/domain"
	"arcticexpress/internal/store"
)

// Service manages dangerous-goods storage locations at the terminal.
type Service struct {
	store *store.Store
}

// New creates a storage service.
func New(s *store.Store) *Service {
	return &Service{store: s}
}

// CreateRequest holds the parameters for creating a storage location.
type CreateRequest struct {
	Zone     string
	Capacity int
}

// Create validates input and persists a new storage location.
func (s *Service) Create(req CreateRequest) (*domain.StorageLocation, error) {
	if req.Zone == "" {
		return nil, errors.New("zone is required")
	}
	if req.Capacity <= 0 {
		return nil, errors.New("capacity must be positive")
	}
	var result *domain.StorageLocation
	err := s.store.Mutate(func(tx *store.Tx) error {
		sl := domain.NewStorageLocation(tx.NextID("SL"), req.Zone, req.Capacity)
		tx.PutStorage(sl)
		result = sl
		return nil
	})
	return result, err
}

// Get retrieves a storage location by ID.
func (s *Service) Get(id string) (*domain.StorageLocation, error) {
	var result *domain.StorageLocation
	err := s.store.View(func(tx *store.Tx) error {
		sl, ok := tx.Storage(id)
		if !ok {
			return fmt.Errorf("storage %s: %w", id, domain.ErrNotFound)
		}
		result = sl
		return nil
	})
	return result, err
}

// List returns all storage locations.
func (s *Service) List() ([]*domain.StorageLocation, error) {
	var result []*domain.StorageLocation
	err := s.store.View(func(tx *store.Tx) error {
		result = tx.AllStorages()
		return nil
	})
	return result, err
}
