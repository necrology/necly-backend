package gormrepo

import (
	"context"
	"errors"
	"testing"

	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
)

// TestNEC89ComingSoonProductStaysVisibleInPublicCatalog proves the flag is
// additive: an active coming-soon product remains in the public catalog with
// the flag set, while an inactive product stays hidden even when it is also
// marked coming soon.
func TestNEC89ComingSoonProductStaysVisibleInPublicCatalog(t *testing.T) {
	fixture := newW7CatalogFixture(t)
	ctx := context.Background()

	for _, productID := range []string{fixture.products["netflix"], fixture.products["canva"]} {
		if err := fixture.db.Model(&domain.Product{}).Where("id = ?", productID).
			Update("coming_soon", true).Error; err != nil {
			t.Fatalf("mark product %s coming soon: %v", productID, err)
		}
	}

	active := true
	products, total, err := fixture.store.ListProductsWithStock(ctx, service.ProductFilter{Active: &active}, 1, 20)
	if err != nil {
		t.Fatalf("public catalog: %v", err)
	}
	// Five active products remain visible; the inactive canva product must not
	// return even though it is coming soon.
	if total != 5 {
		t.Fatalf("public catalog total = %d, want 5 active products", total)
	}
	var netflix *domain.ProductWithStock
	for index := range products {
		if products[index].ID == fixture.products["netflix"] {
			netflix = &products[index]
		}
		if products[index].ID == fixture.products["canva"] {
			t.Fatal("inactive coming-soon product leaked into the public catalog")
		}
	}
	if netflix == nil {
		t.Fatal("active coming-soon product missing from the public catalog")
	}
	if !netflix.ComingSoon {
		t.Fatal("coming_soon flag missing from the public catalog response")
	}

	detail, err := fixture.store.GetProductWithStock(ctx, fixture.products["netflix"])
	if err != nil {
		t.Fatalf("public product detail: %v", err)
	}
	if !detail.ComingSoon {
		t.Fatal("coming_soon flag missing from the public product detail")
	}

	if _, err := fixture.store.GetProductWithStock(ctx, fixture.products["canva"]); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("inactive coming-soon product detail error = %v, want ErrNotFound", err)
	}
}

// TestNEC89CheckoutRejectsComingSoonProductWithoutSideEffects verifies the
// checkout price query refuses a coming-soon product before any order, payment
// or slot mutation is committed.
func TestNEC89CheckoutRejectsComingSoonProductWithoutSideEffects(t *testing.T) {
	fixture := newCheckoutFixture(t)
	if err := fixture.db.Model(&domain.Product{}).Where("id = ?", fixture.product).
		Update("coming_soon", true).Error; err != nil {
		t.Fatalf("mark product coming soon: %v", err)
	}

	provider := &stubProvider{}
	svc := fixture.service(provider)
	_, err := svc.Checkout(context.Background(), fixture.input(fixture.userA, "nec89-coming-soon-0001", ""))
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("checkout error = %v, want ErrNotFound", err)
	}
	if provider.calls.Load() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls.Load())
	}
	assertCheckoutMutationCounts(t, fixture.db, 0, 0, 3)

	var attempts int64
	fixture.db.Model(&domain.CheckoutAttempt{}).Count(&attempts)
	if attempts != 0 {
		t.Fatalf("attempt rows = %d, want 0 after rollback", attempts)
	}
}
