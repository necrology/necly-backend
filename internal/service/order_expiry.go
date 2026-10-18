package service

import (
	"context"
	"log"
	"time"
)

// OrderExpiryRepository expires overdue pending reservations in one
// transaction and returns how many orders changed state. Re-running it is safe:
// the repository only selects orders that are still pending_payment.
type OrderExpiryRepository interface {
	ExpireOverdueOrders(ctx context.Context, now time.Time, limit int) (int, error)
}

type OrderExpiryService struct {
	repo      OrderExpiryRepository
	interval  time.Duration
	batchSize int
	timeout   time.Duration
	now       func() time.Time
}

func NewOrderExpiryService(repo OrderExpiryRepository) *OrderExpiryService {
	return &OrderExpiryService{
		repo:      repo,
		interval:  time.Minute,
		batchSize: 100,
		timeout:   20 * time.Second,
		now:       time.Now,
	}
}

// RunOnce expires at most one bounded batch. It is exported so a test or an
// operator-triggered maintenance command can run the same policy without a
// ticker.
func (s *OrderExpiryService) RunOnce(ctx context.Context) (int, error) {
	runCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.repo.ExpireOverdueOrders(runCtx, s.now().UTC().Truncate(time.Microsecond), s.batchSize)
}

// Run is the bounded background worker. It never holds a database transaction
// across ticks and stops as soon as the application context is cancelled.
func (s *OrderExpiryService) Run(ctx context.Context) {
	if s == nil || s.repo == nil {
		return
	}
	s.runBatch(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runBatch(ctx)
		}
	}
}

func (s *OrderExpiryService) runBatch(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	expired, err := s.RunOnce(ctx)
	if err != nil {
		// The next tick retries; an interrupted batch rolls back in the
		// repository, so there is no partial release to clean up here.
		log.Printf("order expiry worker: %v", err)
		return
	}
	if expired > 0 {
		log.Printf("order expiry worker: expired %d overdue order(s)", expired)
	}
}
