package service

import (
	"context"
	"time"

	"github.com/necly/services/internal/domain"
)

type AdminDashboardService struct {
	repo AdminDashboardRepository
	// now is injectable so range and zero-fill tests do not depend on the wall
	// clock. Production always uses time.Now.
	now func() time.Time
}

func NewAdminDashboardService(repo AdminDashboardRepository) *AdminDashboardService {
	return &AdminDashboardService{repo: repo, now: time.Now}
}

// analyticsDays is the only accepted window for the chart endpoint. A short
// allowlist keeps an arbitrary query from turning into a heavy scan, and makes
// invalid input a client error instead of a silent default.
var analyticsDays = map[int]struct{}{7: {}, 30: {}, 90: {}}

// GetAnalytics returns the dashboard charts for the last days calendar days,
// including today (UTC). Days without orders are filled with zero so a chart
// never has holes. All five queries share the same half-open range.
func (s *AdminDashboardService) GetAnalytics(ctx context.Context, days int) (*DashboardAnalytics, error) {
	if _, ok := analyticsDays[days]; !ok {
		return nil, ErrInvalidInput
	}

	now := s.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	from := today.AddDate(0, 0, -(days - 1))
	to := today.AddDate(0, 0, 1)

	revenue, err := s.repo.GetDailyRevenue(ctx, from, to)
	if err != nil {
		return nil, err
	}
	orders, err := s.repo.GetDailyOrderCounts(ctx, from, to)
	if err != nil {
		return nil, err
	}
	statusBreakdown, err := s.repo.GetStatusBreakdown(ctx, from, to)
	if err != nil {
		return nil, err
	}
	topProducts, err := s.repo.GetTopProductsInRange(ctx, from, to, 5)
	if err != nil {
		return nil, err
	}
	categoryBreakdown, err := s.repo.GetCategoryBreakdown(ctx, from, to)
	if err != nil {
		return nil, err
	}

	revenueByDate := make(map[string]int64, len(revenue))
	for _, point := range revenue {
		revenueByDate[point.Date] = point.Amount
	}
	ordersByDate := make(map[string]int64, len(orders))
	for _, point := range orders {
		ordersByDate[point.Date] = point.Orders
	}

	revenueSeries := make([]RevenuePoint, 0, days)
	ordersSeries := make([]OrderCountPoint, 0, days)
	for i := 0; i < days; i++ {
		date := from.AddDate(0, 0, i).Format("2006-01-02")
		revenueSeries = append(revenueSeries, RevenuePoint{Date: date, Amount: revenueByDate[date]})
		ordersSeries = append(ordersSeries, OrderCountPoint{Date: date, Orders: ordersByDate[date]})
	}

	if statusBreakdown == nil {
		statusBreakdown = []StatusCountPoint{}
	}
	if topProducts == nil {
		topProducts = []TopProduct{}
	}
	if categoryBreakdown == nil {
		categoryBreakdown = []CategoryBreakdownPoint{}
	}

	return &DashboardAnalytics{
		RangeDays:         days,
		From:              from.Format("2006-01-02"),
		To:                today.Format("2006-01-02"),
		RevenueSeries:     revenueSeries,
		OrdersSeries:      ordersSeries,
		StatusBreakdown:   statusBreakdown,
		TopProducts:       topProducts,
		CategoryBreakdown: categoryBreakdown,
	}, nil
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
