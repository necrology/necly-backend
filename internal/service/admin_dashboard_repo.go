package service

import (
	"context"
	"time"

	"github.com/necly/services/internal/domain"
)

type AuditLogFilter struct {
	Action   string
	Entity   string
	EntityID string
	UserID   string
	FromDate string
	ToDate   string
}

type AdminDashboardRepository interface {
	GetStats(ctx context.Context) (*AdminDashboardStats, error)
	GetRevenueData(ctx context.Context, from, to time.Time) ([]RevenuePoint, error)
	GetOrderStats(ctx context.Context, from, to time.Time) (*OrderStats, error)
	GetUserStats(ctx context.Context, from, to time.Time) (*UserStats, error)
	GetTopProducts(ctx context.Context, limit int) ([]TopProduct, error)
	GetRecentOrders(ctx context.Context, limit int) ([]domain.Order, error)
	ListAuditLogs(ctx context.Context, filter AuditLogFilter, page, perPage int) ([]domain.AuditLog, int64, error)
	PingDatabase(ctx context.Context) error
}

type AdminDashboardStats struct {
	TotalUsers    int64 `json:"total_users"`
	TotalOrders   int64 `json:"total_orders"`
	TotalRevenue  int64 `json:"total_revenue"`
	PendingOrders int64 `json:"pending_orders"`
	ActiveUsers   int64 `json:"active_users"`
	NewUsersToday int64 `json:"new_users_today"`
	OrdersToday   int64 `json:"orders_today"`
	RevenueToday  int64 `json:"revenue_today"`
	// ActiveActivations counts allocated inventory slots on orders that are
	// paid or further along the fulfilment chain. AvailableSlots counts the
	// slots still offered for sale. Both are real queries; a failure returns an
	// error instead of a zero that looks like real data.
	ActiveActivations int64 `json:"active_activations"`
	AvailableSlots    int64 `json:"available_slots"`
}

type RevenuePoint struct {
	Date   string `json:"date"`
	Amount int64  `json:"amount"`
}

type OrderStats struct {
	Total      int64 `json:"total"`
	Pending    int64 `json:"pending"`
	Paid       int64 `json:"paid"`
	Fulfilling int64 `json:"fulfilling"`
	Completed  int64 `json:"completed"`
	Cancelled  int64 `json:"cancelled"`
	Expired    int64 `json:"expired"`
	Refunded   int64 `json:"refunded"`
}

type UserStats struct {
	Total    int64 `json:"total"`
	Active   int64 `json:"active"`
	NewToday int64 `json:"new_today"`
	Admins   int64 `json:"admins"`
	Sellers  int64 `json:"sellers"`
}

type TopProduct struct {
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	SalesCount  int64  `json:"sales_count"`
	Revenue     int64  `json:"revenue"`
}
