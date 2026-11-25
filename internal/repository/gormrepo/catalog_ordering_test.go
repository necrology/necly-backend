package gormrepo

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/migrate"
	"github.com/necly/services/internal/platform/testdb"
	"github.com/necly/services/internal/service"
	"path/filepath"
)

// TestListProductsWithStockOrdersActiveBeforeComingSoon_baris130 verifies
// that products which are NOT Coming Soon appear before Coming Soon products,
// and within each group the order is created_at DESC, id ASC.
func TestListProductsWithStockOrdersActiveBeforeComingSoon_baris130(t *testing.T) {
	if !testdb.Available() {
		t.Skip("DB_HOST/DB_USER not set; skipping PostgreSQL integration test")
	}
	db, cleanup, err := testdb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	migrations, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Run(context.Background(), db, migrate.Options{Dir: migrations}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := NewStore(db)
	now := time.Now().UTC()

	// Create a category
	categoryID := uuid.NewString()
	if err := db.Create(&domain.Category{
		ID: categoryID, Name: "Test Cat", Slug: "test-cat", Active: true,
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed category: %v", err)
	}

	// Create 3 active products (not coming soon) with different creation times
	activeProducts := make([]string, 3)
	for i := 0; i < 3; i++ {
		id := uuid.NewString()
		activeProducts[i] = id
		if err := db.Create(&domain.Product{
			ID: id, CategoryID: categoryID,
			Name: "Active Product " + id[:8], Slug: "active-" + id[:8],
			Active: true, ComingSoon: false, CreatedAt: now.Add(-time.Duration(3-i) * time.Hour), UpdatedAt: now,
		}).Error; err != nil {
			t.Fatalf("seed active product %d: %v", i, err)
		}
	}

	// Create 2 coming soon products with different creation times
	comingSoonProducts := make([]string, 2)
	for i := 0; i < 2; i++ {
		id := uuid.NewString()
		comingSoonProducts[i] = id
		if err := db.Create(&domain.Product{
			ID: id, CategoryID: categoryID,
			Name: "Coming Soon Product " + id[:8], Slug: "coming-soon-" + id[:8],
			Active: true, ComingSoon: true, CreatedAt: now.Add(-time.Duration(2-i) * time.Hour), UpdatedAt: now,
		}).Error; err != nil {
			t.Fatalf("seed coming soon product %d: %v", i, err)
		}
	}

	active := true
	products, total, err := store.ListProductsWithStock(context.Background(), service.ProductFilter{Active: &active}, 1, 20)
	if err != nil {
		t.Fatalf("list products: %v", err)
	}

	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(products) != 5 {
		t.Fatalf("len(products) = %d, want 5", len(products))
	}

	// First 3 should be active products (not coming soon), ordered by created_at DESC
	for i := 0; i < 3; i++ {
		if products[i].ComingSoon {
			t.Errorf("products[%d] is coming soon, want active; id=%s name=%s", i, products[i].ID, products[i].Name)
		}
		if products[i].ID != activeProducts[2-i] {
			t.Errorf("products[%d] = %s, want %s (created_at DESC within active group)", i, products[i].ID, activeProducts[2-i])
		}
	}

	// Next 2 should be coming soon products, ordered by created_at DESC
	for i := 0; i < 2; i++ {
		if !products[3+i].ComingSoon {
			t.Errorf("products[%d] is not coming soon, want coming soon; id=%s name=%s", 3+i, products[3+i].ID, products[3+i].Name)
		}
		if products[3+i].ID != comingSoonProducts[1-i] {
			t.Errorf("products[%d] = %s, want %s (created_at DESC within coming soon group)", 3+i, products[3+i].ID, comingSoonProducts[1-i])
		}
	}
}

// TestListProductsWithStockOrderIsDeterministic_baris130 verifies that
// the ordering is stable when multiple products have the same created_at.
func TestListProductsWithStockOrderIsDeterministic_baris130(t *testing.T) {
	if !testdb.Available() {
		t.Skip("DB_HOST/DB_USER not set; skipping PostgreSQL integration test")
	}
	db, cleanup, err := testdb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	migrations, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Run(context.Background(), db, migrate.Options{Dir: migrations}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := NewStore(db)
	now := time.Now().UTC()

	categoryID := uuid.NewString()
	if err := db.Create(&domain.Category{
		ID: categoryID, Name: "Test Cat", Slug: "test-cat", Active: true,
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed category: %v", err)
	}

	// Create 3 active products with the SAME created_at but different IDs
	ids := make([]string, 3)
	for i := 0; i < 3; i++ {
		ids[i] = uuid.NewString()
		if err := db.Create(&domain.Product{
			ID: ids[i], CategoryID: categoryID,
			Name: "Product " + ids[i][:8], Slug: "prod-" + ids[i][:8],
			Active: true, ComingSoon: false, CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatalf("seed product %d: %v", i, err)
		}
	}

	active := true
	products, _, err := store.ListProductsWithStock(context.Background(), service.ProductFilter{Active: &active}, 1, 20)
	if err != nil {
		t.Fatalf("list products: %v", err)
	}

	// Should be ordered by id ASC as tiebreaker
	for i := 0; i < 3; i++ {
		if products[i].ID != ids[i] {
			t.Errorf("products[%d] = %s, want %s (id ASC tiebreaker)", i, products[i].ID, ids[i])
		}
	}

	// Run again to verify stability
	products2, _, err := store.ListProductsWithStock(context.Background(), service.ProductFilter{Active: &active}, 1, 20)
	if err != nil {
		t.Fatalf("list products second run: %v", err)
	}
	for i := 0; i < 3; i++ {
		if products2[i].ID != products[i].ID {
			t.Errorf("order not stable: run1[%d]=%s run2[%d]=%s", i, products[i].ID, i, products2[i].ID)
		}
	}
}
