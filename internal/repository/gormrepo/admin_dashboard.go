package gormrepo

import (
	"context"
	"fmt"
	"time"

	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
)

func (s *Store) GetStats(ctx context.Context) (*service.AdminDashboardStats, error) {
	now := s.nowUTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	var stats service.AdminDashboardStats

	// Total users
	if err := s.db.WithContext(ctx).Model(&domain.User{}).Where("deleted_at IS NULL").Count(&stats.TotalUsers).Error; err != nil {
		return nil, fmt.Errorf("count total users: %w", err)
	}

	// Total orders
	if err := s.db.WithContext(ctx).Model(&domain.Order{}).Count(&stats.TotalOrders).Error; err != nil {
		return nil, fmt.Errorf("count total orders: %w", err)
	}

	// Total revenue
	if err := s.db.WithContext(ctx).Model(&domain.Order{}).Where("status IN ?", []string{"paid", "fulfilling", "completed"}).Select("COALESCE(SUM(total), 0)").Scan(&stats.TotalRevenue).Error; err != nil {
		return nil, fmt.Errorf("sum total revenue: %w", err)
	}

	// Pending orders
	if err := s.db.WithContext(ctx).Model(&domain.Order{}).Where("status = ?", domain.OrderPending).Count(&stats.PendingOrders).Error; err != nil {
		return nil, fmt.Errorf("count pending orders: %w", err)
	}

	// Active users (logged in within 30 days)
	thirtyDaysAgo := now.AddDate(0, 0, -30)
	if err := s.db.WithContext(ctx).Model(&domain.User{}).Where("deleted_at IS NULL AND updated_at >= ?", thirtyDaysAgo).Count(&stats.ActiveUsers).Error; err != nil {
		return nil, fmt.Errorf("count active users: %w", err)
	}

	// New users today
	if err := s.db.WithContext(ctx).Model(&domain.User{}).Where("deleted_at IS NULL AND created_at >= ?", todayStart).Count(&stats.NewUsersToday).Error; err != nil {
		return nil, fmt.Errorf("count new users today: %w", err)
	}

	// Orders today
	if err := s.db.WithContext(ctx).Model(&domain.Order{}).Where("created_at >= ?", todayStart).Count(&stats.OrdersToday).Error; err != nil {
		return nil, fmt.Errorf("count orders today: %w", err)
	}

	// Revenue today
	if err := s.db.WithContext(ctx).Model(&domain.Order{}).Where("created_at >= ? AND status IN ?", todayStart, []string{"paid", "fulfilling", "completed"}).Select("COALESCE(SUM(total), 0)").Scan(&stats.RevenueToday).Error; err != nil {
		return nil, fmt.Errorf("sum revenue today: %w", err)
	}

	return &stats, nil
}

func (s *Store) GetRevenueData(ctx context.Context, from, to time.Time) ([]service.RevenuePoint, error) {
	type result struct {
		Date   string
		Amount int64
	}
	var results []result
	err := s.db.WithContext(ctx).Model(&domain.Order{}).
		Where("created_at >= ? AND created_at <= ? AND status IN ?", from, to, []string{"paid", "fulfilling", "completed"}).
		Select("DATE(created_at) as date, COALESCE(SUM(total), 0) as amount").
		Group("DATE(created_at)").
		Order("DATE(created_at) ASC").
		Scan(&results).Error
	if err != nil {
		return nil, fmt.Errorf("get revenue data: %w", err)
	}

	points := make([]service.RevenuePoint, len(results))
	for i, r := range results {
		points[i] = service.RevenuePoint{Date: r.Date, Amount: r.Amount}
	}
	return points, nil
}

func (s *Store) GetOrderStats(ctx context.Context, from, to time.Time) (*service.OrderStats, error) {
	var stats service.OrderStats

	type statusCount struct {
		Status string
		Count  int64
	}
	var counts []statusCount
	err := s.db.WithContext(ctx).Model(&domain.Order{}).
		Where("created_at >= ? AND created_at <= ?", from, to).
		Select("status, COUNT(*) as count").
		Group("status").
		Scan(&counts).Error
	if err != nil {
		return nil, fmt.Errorf("get order stats: %w", err)
	}

	for _, c := range counts {
		switch domain.OrderStatus(c.Status) {
		case domain.OrderPending:
			stats.Pending = c.Count
		case domain.OrderPaid:
			stats.Paid = c.Count
		case domain.OrderFulfilling:
			stats.Fulfilling = c.Count
		case domain.OrderCompleted:
			stats.Completed = c.Count
		case domain.OrderCancelled:
			stats.Cancelled = c.Count
		case domain.OrderExpired:
			stats.Expired = c.Count
		case domain.OrderRefunded:
			stats.Refunded = c.Count
		}
		stats.Total += c.Count
	}

	return &stats, nil
}

func (s *Store) GetUserStats(ctx context.Context, from, to time.Time) (*service.UserStats, error) {
	var stats service.UserStats

	// Total users
	if err := s.db.WithContext(ctx).Model(&domain.User{}).Where("deleted_at IS NULL").Count(&stats.Total).Error; err != nil {
		return nil, fmt.Errorf("count total users: %w", err)
	}

	// Active users
	if err := s.db.WithContext(ctx).Model(&domain.User{}).Where("deleted_at IS NULL AND updated_at >= ?", from).Count(&stats.Active).Error; err != nil {
		return nil, fmt.Errorf("count active users: %w", err)
	}

	// New users today
	todayStart := time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC)
	if err := s.db.WithContext(ctx).Model(&domain.User{}).Where("deleted_at IS NULL AND created_at >= ?", todayStart).Count(&stats.NewToday).Error; err != nil {
		return nil, fmt.Errorf("count new users today: %w", err)
	}

	// Admin users
	if err := s.db.WithContext(ctx).Model(&domain.User{}).Where("deleted_at IS NULL AND role = ?", domain.RoleAdmin).Count(&stats.Admins).Error; err != nil {
		return nil, fmt.Errorf("count admin users: %w", err)
	}

	// Seller users
	if err := s.db.WithContext(ctx).Model(&domain.User{}).Where("deleted_at IS NULL AND role = ?", domain.RoleSeller).Count(&stats.Sellers).Error; err != nil {
		return nil, fmt.Errorf("count seller users: %w", err)
	}

	return &stats, nil
}

func (s *Store) GetTopProducts(ctx context.Context, limit int) ([]service.TopProduct, error) {
	type result struct {
		ProductID   string
		ProductName string
		SalesCount  int64
		Revenue     int64
	}
	var results []result
	err := s.db.WithContext(ctx).Model(&domain.OrderItem{}).
		Joins("JOIN orders ON order_items.order_id = orders.id").
		Where("orders.status IN ?", []string{"paid", "fulfilling", "completed"}).
		Select("order_items.product_price_id as product_id, order_items.product_name as product_name, SUM(order_items.quantity) as sales_count, SUM(order_items.line_total) as revenue").
		Group("order_items.product_price_id, order_items.product_name").
		Order("revenue DESC").
		Limit(limit).
		Scan(&results).Error
	if err != nil {
		return nil, fmt.Errorf("get top products: %w", err)
	}

	products := make([]service.TopProduct, len(results))
	for i, r := range results {
		products[i] = service.TopProduct{
			ProductID:   r.ProductID,
			ProductName: r.ProductName,
			SalesCount:  r.SalesCount,
			Revenue:     r.Revenue,
		}
	}
	return products, nil
}

func (s *Store) GetRecentOrders(ctx context.Context, limit int) ([]domain.Order, error) {
	var orders []domain.Order
	err := s.db.WithContext(ctx).Preload("Customer").Preload("Items").Preload("Payments").
		Order("created_at DESC").
		Limit(limit).
		Find(&orders).Error
	if err != nil {
		return nil, fmt.Errorf("get recent orders: %w", err)
	}
	return orders, nil
}

func (s *Store) PingDatabase(ctx context.Context) error {
	return s.db.WithContext(ctx).Exec("SELECT 1").Error
}
