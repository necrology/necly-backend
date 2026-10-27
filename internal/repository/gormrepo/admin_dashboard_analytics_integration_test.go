package gormrepo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/migrate"
	"github.com/necly/services/internal/platform/testdb"
)

// TestDashboardAnalyticsRepositoryUsesHalfOpenUTCRangeAndRealAggregates proves
// the chart queries use the same [from, to) window, exclude non-revenue orders
// from revenue, count every status in the order series, and count an order once
// per category even when it has several items.
func TestDashboardAnalyticsRepositoryUsesHalfOpenUTCRangeAndRealAggregates(t *testing.T) {
	if !testdb.Available() {
		t.Skip("DB_HOST/DB_USER not set; skipping PostgreSQL integration test")
	}
	db, cleanup, err := testdb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Run(context.Background(), db, migrate.Options{Dir: dir}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := NewStore(db)
	ctx := context.Background()

	user := &domain.User{
		ID: uuid.NewString(), Email: "analytics-" + uuid.NewString() + "@example.test",
		Name: "Analytics Fixture", PasswordHash: "not-used", Role: domain.RoleCustomer, Active: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("insert user: %v", err)
	}
	duration := &domain.ProductDuration{ID: uuid.NewString(), Name: "1 Bulan", Days: 30}
	if err := db.Create(duration).Error; err != nil {
		t.Fatalf("insert duration: %v", err)
	}

	videoCategory := &domain.Category{ID: uuid.NewString(), Name: "Streaming Video", Slug: "video-" + uuid.NewString(), Active: true}
	cloudCategory := &domain.Category{ID: uuid.NewString(), Name: "Penyimpanan Awan", Slug: "cloud-" + uuid.NewString(), Active: true}
	for _, category := range []*domain.Category{videoCategory, cloudCategory} {
		if err := db.Create(category).Error; err != nil {
			t.Fatalf("insert category: %v", err)
		}
	}

	videoProduct := &domain.Product{ID: uuid.NewString(), CategoryID: videoCategory.ID, Name: "Netflix", Slug: "netflix-" + uuid.NewString(), Active: true}
	cloudProduct := &domain.Product{ID: uuid.NewString(), CategoryID: cloudCategory.ID, Name: "Google One", Slug: "google-one-" + uuid.NewString(), Active: true}
	for _, product := range []*domain.Product{videoProduct, cloudProduct} {
		if err := db.Create(product).Error; err != nil {
			t.Fatalf("insert product: %v", err)
		}
	}

	videoPrice := &domain.ProductPrice{ID: uuid.NewString(), ProductID: videoProduct.ID, DurationID: duration.ID, Amount: 50000, Currency: "IDR", Active: true}
	cloudPrice := &domain.ProductPrice{ID: uuid.NewString(), ProductID: cloudProduct.ID, DurationID: duration.ID, Amount: 60000, Currency: "IDR", Active: true}
	for _, price := range []*domain.ProductPrice{videoPrice, cloudPrice} {
		if err := db.Create(price).Error; err != nil {
			t.Fatalf("insert price: %v", err)
		}
	}

	from := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	orders := []struct {
		status   domain.OrderStatus
		at       time.Time
		total    int64
		price    *domain.ProductPrice
		quantity int
		line     int64
	}{
		{domain.OrderPaid, from, 100000, videoPrice, 2, 100000},
		{domain.OrderPaid, time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), 60000, cloudPrice, 1, 60000},
		{domain.OrderPending, time.Date(2026, 10, 22, 13, 0, 0, 0, time.UTC), 50000, videoPrice, 1, 50000},
		{domain.OrderCompleted, time.Date(2026, 10, 27, 23, 59, 59, 0, time.UTC), 40000, videoPrice, 3, 40000},
		// Exactly on the exclusive upper bound: must not leak into the window.
		{domain.OrderPaid, to, 99999, videoPrice, 1, 99999},
	}
	for index, fixture := range orders {
		order := &domain.Order{
			ID: uuid.NewString(), Number: "ORD-ANALYTICS-" + uuid.NewString(), UserID: user.ID,
			Status: fixture.status, Subtotal: fixture.total, Total: fixture.total, Currency: "IDR",
			PaymentDueAt: fixture.at.Add(15 * time.Minute), IdempotencyKey: "analytics-" + uuid.NewString(),
			CreatedAt: fixture.at, UpdatedAt: fixture.at,
		}
		if err := db.Create(order).Error; err != nil {
			t.Fatalf("insert order %d: %v", index, err)
		}
		item := &domain.OrderItem{
			ID: uuid.NewString(), OrderID: order.ID, ProductPriceID: fixture.price.ID,
			ProductName: fixture.price.ProductID, DurationName: duration.Name,
			UnitPrice: fixture.line / int64(fixture.quantity), Quantity: fixture.quantity, LineTotal: fixture.line,
		}
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("insert order item %d: %v", index, err)
		}
	}

	revenue, err := store.GetDailyRevenue(ctx, from, to)
	if err != nil {
		t.Fatalf("daily revenue: %v", err)
	}
	if len(revenue) != 3 || revenue[0].Date != "2026-10-21" || revenue[0].Amount != 100000 ||
		revenue[1].Date != "2026-10-22" || revenue[1].Amount != 60000 ||
		revenue[2].Date != "2026-10-27" || revenue[2].Amount != 40000 {
		t.Fatalf("daily revenue = %#v, want 21/100000, 22/60000, 27/40000", revenue)
	}

	orderCounts, err := store.GetDailyOrderCounts(ctx, from, to)
	if err != nil {
		t.Fatalf("daily order counts: %v", err)
	}
	if len(orderCounts) != 3 || orderCounts[0].Date != "2026-10-21" || orderCounts[0].Orders != 1 ||
		orderCounts[1].Date != "2026-10-22" || orderCounts[1].Orders != 2 ||
		orderCounts[2].Date != "2026-10-27" || orderCounts[2].Orders != 1 {
		t.Fatalf("daily order counts = %#v, want 21/1, 22/2, 27/1", orderCounts)
	}

	statuses, err := store.GetStatusBreakdown(ctx, from, to)
	if err != nil {
		t.Fatalf("status breakdown: %v", err)
	}
	statusCounts := map[string]int64{}
	for _, status := range statuses {
		statusCounts[status.Status] = status.Count
	}
	if statusCounts[string(domain.OrderPaid)] != 2 || statusCounts[string(domain.OrderPending)] != 1 || statusCounts[string(domain.OrderCompleted)] != 1 {
		t.Fatalf("status breakdown = %#v, want paid 2, pending 1, completed 1", statuses)
	}

	top, err := store.GetTopProductsInRange(ctx, from, to, 5)
	if err != nil {
		t.Fatalf("top products: %v", err)
	}
	if len(top) != 2 || top[0].ProductID != videoPrice.ID || top[0].SalesCount != 5 || top[0].Revenue != 140000 ||
		top[1].ProductID != cloudPrice.ID || top[1].SalesCount != 1 || top[1].Revenue != 60000 {
		t.Fatalf("top products = %#v, want video 5/140000 then cloud 1/60000", top)
	}

	categories, err := store.GetCategoryBreakdown(ctx, from, to)
	if err != nil {
		t.Fatalf("category breakdown: %v", err)
	}
	if len(categories) != 2 || categories[0].CategoryID != videoCategory.ID || categories[0].Orders != 2 || categories[0].Revenue != 140000 ||
		categories[1].CategoryID != cloudCategory.ID || categories[1].Orders != 1 || categories[1].Revenue != 60000 {
		t.Fatalf("category breakdown = %#v, want video 2/140000 then cloud 1/60000", categories)
	}
}
