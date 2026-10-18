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

// TestOrderModelMatchesMigratedSchema proves the GORM model and the SQL chain
// agree. GORM only ever writes columns it knows about, so a missing migration
// column surfaced here as a real INSERT failure instead of an AutoMigrate
// silently repairing production.
func TestOrderModelMatchesMigratedSchema(t *testing.T) {
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

	user := &domain.User{
		ID: uuid.NewString(), Email: "schema-fixture@example.test", Name: "Schema Fixture",
		PasswordHash: "not-used", Role: domain.RoleCustomer, Active: true,
		EmailVerifiedAt: ptrTime(time.Now().UTC()), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("insert user: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	order := &domain.Order{
		ID: uuid.NewString(), Number: "ORD-SCHEMA-FIXTURE", UserID: user.ID,
		Status: domain.OrderPending, Subtotal: 186000, Discount: 0, Total: 186000,
		Currency: "IDR", PaymentDueAt: now.Add(15 * time.Minute),
		ShippingAddress: "Jl. Contoh 1", Notes: "Titip di resepsionis",
		IdempotencyKey: "schema-fixture-key", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatalf("insert order with compatibility columns: %v", err)
	}

	var stored domain.Order
	if err := db.Where("id = ?", order.ID).First(&stored).Error; err != nil {
		t.Fatalf("read order: %v", err)
	}
	if stored.ShippingAddress != order.ShippingAddress || stored.Notes != order.Notes {
		t.Fatalf("compatibility columns did not round-trip: %#v", stored)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
