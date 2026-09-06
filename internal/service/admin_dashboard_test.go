package service

import (
	"context"
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
