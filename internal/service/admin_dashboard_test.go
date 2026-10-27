package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockAdminDashboardRepository struct {
	mock.Mock
}

func (m *mockAdminDashboardRepository) GetStats(ctx context.Context) (*AdminDashboardStats, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*AdminDashboardStats), args.Error(1)
}

func (m *mockAdminDashboardRepository) GetRevenueData(ctx context.Context, from, to time.Time) ([]RevenuePoint, error) {
	args := m.Called(ctx, from, to)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]RevenuePoint), args.Error(1)
}

func (m *mockAdminDashboardRepository) GetOrderStats(ctx context.Context, from, to time.Time) (*OrderStats, error) {
	args := m.Called(ctx, from, to)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*OrderStats), args.Error(1)
}

func (m *mockAdminDashboardRepository) GetUserStats(ctx context.Context, from, to time.Time) (*UserStats, error) {
	args := m.Called(ctx, from, to)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*UserStats), args.Error(1)
}

func (m *mockAdminDashboardRepository) GetTopProducts(ctx context.Context, limit int) ([]TopProduct, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]TopProduct), args.Error(1)
}

func (m *mockAdminDashboardRepository) GetDailyRevenue(ctx context.Context, from, to time.Time) ([]RevenuePoint, error) {
	args := m.Called(ctx, from, to)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]RevenuePoint), args.Error(1)
}

func (m *mockAdminDashboardRepository) GetDailyOrderCounts(ctx context.Context, from, to time.Time) ([]OrderCountPoint, error) {
	args := m.Called(ctx, from, to)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]OrderCountPoint), args.Error(1)
}

func (m *mockAdminDashboardRepository) GetStatusBreakdown(ctx context.Context, from, to time.Time) ([]StatusCountPoint, error) {
	args := m.Called(ctx, from, to)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]StatusCountPoint), args.Error(1)
}

func (m *mockAdminDashboardRepository) GetTopProductsInRange(ctx context.Context, from, to time.Time, limit int) ([]TopProduct, error) {
	args := m.Called(ctx, from, to, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]TopProduct), args.Error(1)
}

func (m *mockAdminDashboardRepository) GetCategoryBreakdown(ctx context.Context, from, to time.Time) ([]CategoryBreakdownPoint, error) {
	args := m.Called(ctx, from, to)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]CategoryBreakdownPoint), args.Error(1)
}

func (m *mockAdminDashboardRepository) GetRecentOrders(ctx context.Context, limit int) ([]domain.Order, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.Order), args.Error(1)
}

func (m *mockAdminDashboardRepository) ListAuditLogs(ctx context.Context, filter AuditLogFilter, page, perPage int) ([]domain.AuditLog, int64, error) {
	args := m.Called(ctx, filter, page, perPage)
	if args.Get(0) == nil {
		return nil, 0, args.Error(1)
	}
	return args.Get(0).([]domain.AuditLog), args.Get(1).(int64), args.Error(2)
}

func (m *mockAdminDashboardRepository) PingDatabase(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func TestAdminDashboardService_GetDashboardStats(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminDashboardRepository)
	svc := NewAdminDashboardService(repo)

	expected := &AdminDashboardStats{
		TotalUsers:    100,
		TotalOrders:   50,
		TotalRevenue:  5000000,
		PendingOrders: 10,
		ActiveUsers:   80,
		NewUsersToday: 5,
		OrdersToday:   3,
		RevenueToday:  150000,
	}

	repo.On("GetStats", ctx).Return(expected, nil)

	stats, err := svc.GetDashboardStats(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int64(100), stats.TotalUsers)
	assert.Equal(t, int64(50), stats.TotalOrders)
	repo.AssertExpectations(t)
}

func TestAdminDashboardService_GetRevenueData(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminDashboardRepository)
	svc := NewAdminDashboardService(repo)

	expected := []RevenuePoint{
		{Date: "2024-01-01", Amount: 100000},
		{Date: "2024-01-02", Amount: 200000},
	}

	repo.On("GetRevenueData", ctx, mock.AnythingOfType("time.Time"), mock.AnythingOfType("time.Time")).Return(expected, nil)

	data, err := svc.GetRevenueData(ctx, 30)
	assert.NoError(t, err)
	assert.Len(t, data, 2)
	repo.AssertExpectations(t)
}

func TestAdminDashboardService_GetOrderStats(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminDashboardRepository)
	svc := NewAdminDashboardService(repo)

	expected := &OrderStats{
		Total:      50,
		Pending:    10,
		Paid:       20,
		Fulfilling: 5,
		Completed:  10,
		Cancelled:  3,
		Expired:    1,
		Refunded:   1,
	}

	repo.On("GetOrderStats", ctx, mock.AnythingOfType("time.Time"), mock.AnythingOfType("time.Time")).Return(expected, nil)

	stats, err := svc.GetOrderStats(ctx, 30)
	assert.NoError(t, err)
	assert.Equal(t, int64(50), stats.Total)
	repo.AssertExpectations(t)
}

func TestAdminDashboardService_GetUserStats(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminDashboardRepository)
	svc := NewAdminDashboardService(repo)

	expected := &UserStats{
		Total:    100,
		Active:   80,
		NewToday: 5,
		Admins:   2,
		Sellers:  10,
	}

	repo.On("GetUserStats", ctx, mock.AnythingOfType("time.Time"), mock.AnythingOfType("time.Time")).Return(expected, nil)

	stats, err := svc.GetUserStats(ctx, 30)
	assert.NoError(t, err)
	assert.Equal(t, int64(100), stats.Total)
	repo.AssertExpectations(t)
}

func TestAdminDashboardService_GetTopProducts(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminDashboardRepository)
	svc := NewAdminDashboardService(repo)

	expected := []TopProduct{
		{ProductID: uuid.NewString(), ProductName: "Product 1", SalesCount: 50, Revenue: 5000000},
		{ProductID: uuid.NewString(), ProductName: "Product 2", SalesCount: 30, Revenue: 3000000},
	}

	repo.On("GetTopProducts", ctx, 10).Return(expected, nil)

	products, err := svc.GetTopProducts(ctx, 10)
	assert.NoError(t, err)
	assert.Len(t, products, 2)
	repo.AssertExpectations(t)
}

func TestAdminDashboardService_GetTopProducts_DefaultLimit(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminDashboardRepository)
	svc := NewAdminDashboardService(repo)

	expected := []TopProduct{
		{ProductID: uuid.NewString(), ProductName: "Product 1", SalesCount: 50, Revenue: 5000000},
	}

	repo.On("GetTopProducts", ctx, 10).Return(expected, nil)

	products, err := svc.GetTopProducts(ctx, 0)
	assert.NoError(t, err)
	assert.Len(t, products, 1)
	repo.AssertExpectations(t)
}

func TestAdminDashboardService_GetTopProducts_MaxLimit(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminDashboardRepository)
	svc := NewAdminDashboardService(repo)

	repo.On("GetTopProducts", ctx, 100).Return([]TopProduct{}, nil)

	_, err := svc.GetTopProducts(ctx, 200)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestAdminDashboardService_GetRecentOrders(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminDashboardRepository)
	svc := NewAdminDashboardService(repo)

	expected := []domain.Order{
		{ID: uuid.NewString(), Number: "NEC-001", Status: domain.OrderPaid},
		{ID: uuid.NewString(), Number: "NEC-002", Status: domain.OrderCompleted},
	}

	repo.On("GetRecentOrders", ctx, 10).Return(expected, nil)

	orders, err := svc.GetRecentOrders(ctx, 10)
	assert.NoError(t, err)
	assert.Len(t, orders, 2)
	repo.AssertExpectations(t)
}

func TestAdminDashboardService_GetAnalytics_RejectsInvalidDays(t *testing.T) {
	ctx := context.Background()
	for _, days := range []int{0, 1, 6, 8, 14, 31, 89, 91, -7} {
		t.Run(fmt.Sprintf("days_%d", days), func(t *testing.T) {
			repo := new(mockAdminDashboardRepository)
			svc := NewAdminDashboardService(repo)
			svc.now = func() time.Time { return time.Date(2026, 10, 27, 15, 4, 5, 0, time.UTC) }

			analytics, err := svc.GetAnalytics(ctx, days)
			assert.Nil(t, analytics)
			assert.ErrorIs(t, err, ErrInvalidInput)
			repo.AssertNotCalled(t, "GetDailyRevenue", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestAdminDashboardService_GetAnalytics_UsesInclusiveUTCWindow(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		days int
		from time.Time
		to   time.Time
	}{
		{7, time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)},
		{30, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)},
		{90, time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("days_%d", tc.days), func(t *testing.T) {
			repo := new(mockAdminDashboardRepository)
			svc := NewAdminDashboardService(repo)
			// A non-midnight clock proves the window snaps to the UTC day.
			svc.now = func() time.Time { return time.Date(2026, 10, 27, 23, 59, 59, 0, time.UTC) }
			repo.On("GetDailyRevenue", ctx, tc.from, tc.to).Return([]RevenuePoint{}, nil)
			repo.On("GetDailyOrderCounts", ctx, tc.from, tc.to).Return([]OrderCountPoint{}, nil)
			repo.On("GetStatusBreakdown", ctx, tc.from, tc.to).Return([]StatusCountPoint{}, nil)
			repo.On("GetTopProductsInRange", ctx, tc.from, tc.to, 5).Return([]TopProduct{}, nil)
			repo.On("GetCategoryBreakdown", ctx, tc.from, tc.to).Return([]CategoryBreakdownPoint{}, nil)

			analytics, err := svc.GetAnalytics(ctx, tc.days)
			assert.NoError(t, err)
			assert.Equal(t, tc.days, analytics.RangeDays)
			assert.Equal(t, tc.from.Format("2006-01-02"), analytics.From)
			assert.Equal(t, "2026-10-27", analytics.To)
			assert.Len(t, analytics.RevenueSeries, tc.days)
			assert.Len(t, analytics.OrdersSeries, tc.days)
			repo.AssertExpectations(t)
		})
	}
}

func TestAdminDashboardService_GetAnalytics_ZeroFillsMissingDays(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminDashboardRepository)
	svc := NewAdminDashboardService(repo)
	svc.now = func() time.Time { return time.Date(2026, 10, 27, 9, 30, 0, 0, time.UTC) }

	from := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)

	revenue := []RevenuePoint{
		{Date: "2026-10-21", Amount: 100000},
		{Date: "2026-10-27", Amount: 250000},
	}
	orders := []OrderCountPoint{
		{Date: "2026-10-26", Orders: 3},
	}
	statuses := []StatusCountPoint{
		{Status: "paid", Count: 2},
		{Status: "pending_payment", Count: 1},
	}
	top := []TopProduct{
		{ProductID: "price-1", ProductName: "Netflix", SalesCount: 4, Revenue: 744000},
	}
	categories := []CategoryBreakdownPoint{
		{CategoryID: "cat-1", CategoryName: "Streaming Video", Orders: 3, Revenue: 744000},
	}

	repo.On("GetDailyRevenue", ctx, from, to).Return(revenue, nil)
	repo.On("GetDailyOrderCounts", ctx, from, to).Return(orders, nil)
	repo.On("GetStatusBreakdown", ctx, from, to).Return(statuses, nil)
	repo.On("GetTopProductsInRange", ctx, from, to, 5).Return(top, nil)
	repo.On("GetCategoryBreakdown", ctx, from, to).Return(categories, nil)

	analytics, err := svc.GetAnalytics(ctx, 7)
	assert.NoError(t, err)
	assert.Equal(t, 7, analytics.RangeDays)
	assert.Equal(t, "2026-10-21", analytics.From)
	assert.Equal(t, "2026-10-27", analytics.To)

	assert.Equal(t, []RevenuePoint{
		{Date: "2026-10-21", Amount: 100000},
		{Date: "2026-10-22", Amount: 0},
		{Date: "2026-10-23", Amount: 0},
		{Date: "2026-10-24", Amount: 0},
		{Date: "2026-10-25", Amount: 0},
		{Date: "2026-10-26", Amount: 0},
		{Date: "2026-10-27", Amount: 250000},
	}, analytics.RevenueSeries)
	assert.Equal(t, []OrderCountPoint{
		{Date: "2026-10-21", Orders: 0},
		{Date: "2026-10-22", Orders: 0},
		{Date: "2026-10-23", Orders: 0},
		{Date: "2026-10-24", Orders: 0},
		{Date: "2026-10-25", Orders: 0},
		{Date: "2026-10-26", Orders: 3},
		{Date: "2026-10-27", Orders: 0},
	}, analytics.OrdersSeries)
	assert.Equal(t, statuses, analytics.StatusBreakdown)
	assert.Equal(t, top, analytics.TopProducts)
	assert.Equal(t, categories, analytics.CategoryBreakdown)
	repo.AssertExpectations(t)
}

func TestAdminDashboardService_GetAnalytics_PropagatesRepositoryError(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminDashboardRepository)
	svc := NewAdminDashboardService(repo)
	svc.now = func() time.Time { return time.Date(2026, 10, 27, 9, 30, 0, 0, time.UTC) }

	from := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	repo.On("GetDailyRevenue", ctx, from, to).Return(nil, errors.New("query failed"))

	analytics, err := svc.GetAnalytics(ctx, 7)
	assert.Nil(t, analytics)
	assert.EqualError(t, err, "query failed")
	repo.AssertNotCalled(t, "GetDailyOrderCounts", mock.Anything, mock.Anything, mock.Anything)
}
