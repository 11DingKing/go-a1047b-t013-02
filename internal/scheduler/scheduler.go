package scheduler

import (
	"context"
	"time"

	"arcticexpress/internal/booking"
	"arcticexpress/internal/domain"
	"arcticexpress/internal/store"
)

// Config holds scheduler tuning parameters.
type Config struct {
	Interval        time.Duration
	CutoffThreshold time.Duration
}

// DefaultConfig returns sensible production defaults.
func DefaultConfig() Config {
	return Config{
		Interval:        30 * time.Second,
		CutoffThreshold: 48 * time.Hour,
	}
}

// Service runs background maintenance tasks: releasing expired holds and
// flagging cutoff-non-compliant dangerous cargo.
type Service struct {
	store   *store.Store
	booking *booking.Service
	clock   domain.Clock
	cfg     Config
}

// New creates a scheduler.
func New(s *store.Store, bs *booking.Service, clock domain.Clock, cfg Config) *Service {
	return &Service{store: s, booking: bs, clock: clock, cfg: cfg}
}

// Run starts the periodic loop until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.CheckDepositTimeouts()
			s.CheckCutoffCompliance()
		}
	}
}

// CheckDepositTimeouts releases holds whose deposit deadline has elapsed.
func (s *Service) CheckDepositTimeouts() {
	now := s.clock.Now()
	var expired []string
	_ = s.store.View(func(tx *store.Tx) error {
		for _, v := range tx.AllVoyages() {
			expired = append(expired, v.ExpiredHolds(now)...)
		}
		return nil
	})
	for _, id := range expired {
		_ = s.booking.CancelExpiredHold(id)
	}
}

// CheckCutoffCompliance cancels dangerous-cargo bookings that breach the
// cutoff threshold without a DG certificate or locked storage.
func (s *Service) CheckCutoffCompliance() {
	now := s.clock.Now()
	var nonCompliant []struct {
		id     string
		reason string
	}
	_ = s.store.View(func(tx *store.Tx) error {
		for _, b := range tx.AllBookings() {
			if b.State != domain.StateDepositPaid && b.State != domain.StateCustomsReleased {
				continue
			}
			v, ok := tx.Voyage(b.VoyageID)
			if !ok {
				continue
			}
			if err := b.CutoffCompliant(now, v.CutoffAt, s.cfg.CutoffThreshold); err != nil {
				nonCompliant = append(nonCompliant, struct {
					id     string
					reason string
				}{b.ID, err.Error()})
			}
		}
		return nil
	})
	for _, nc := range nonCompliant {
		_ = s.booking.CancelForCutoffNonCompliance(nc.id, "cutoff non-compliance: "+nc.reason)
	}
}

// RunOnce executes both checks once; useful for tests.
func (s *Service) RunOnce() {
	s.CheckDepositTimeouts()
	s.CheckCutoffCompliance()
}
