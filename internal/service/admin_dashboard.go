package service

import (
	"context"
	"time"

	"github.com/necly/services/internal/domain"
)

type AdminDashboardService struct {
	repo AdminDashboardRepository
}

func NewAdminDashboardService(repo AdminDashboardRepository) *AdminDashboardService {
	return &AdminDashboardService{repo: repo}
}

func (s *AdminDashboardService) GetDashboardStats(ctx context.Context) (*AdminDashboardStats, error) {
	return s.repo.GetStats(ctx)
}

func (s *AdminDashboardService) GetRevenueData(ctx context.Context, days int) ([]RevenuePoint, error) {
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -days)
	to := now
	return s.repo.GetRevenueData(ctx, from, to)
}

func (s *AdminDashboardService) GetOrderStats(ctx context.Context, days int) (*OrderStats, error) {
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -days)
	to := now
	return s.repo.GetOrderStats(ctx, from, to)
}

func (s *AdminDashboardService) GetUserStats(ctx context.Context, days int) (*UserStats, error) {
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -days)
	to := now
	return s.repo.GetUserStats(ctx, from, to)
}

func (s *AdminDashboardService) GetTopProducts(ctx context.Context, limit int) ([]TopProduct, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	return s.repo.GetTopProducts(ctx, limit)
}

func (s *AdminDashboardService) GetRecentOrders(ctx context.Context, limit int) ([]domain.Order, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	return s.repo.GetRecentOrders(ctx, limit)
}

func (s *AdminDashboardService) ListAuditLogs(ctx context.Context, filter AuditLogFilter, page, perPage int) ([]domain.AuditLog, int64, error) {
	return s.repo.ListAuditLogs(ctx, filter, page, perPage)
}

func (s *AdminDashboardService) PingDatabase(ctx context.Context) error {
	return s.repo.PingDatabase(ctx)
}
