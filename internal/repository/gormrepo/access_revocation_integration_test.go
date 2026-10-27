package gormrepo

import (
	"context"
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
)

// deliverOrder marks every item delivery as sent and activates the buyer access
// window, mirroring a successful admin email delivery without invoking SMTP.
func deliverOrder(t *testing.T, fixture *checkoutFixture, order *domain.Order, now time.Time) {
	t.Helper()
	ctx := context.Background()
	rows, err := fixture.store.EnsureOrderDeliveries(ctx, order.ID, now)
	if err != nil {
		t.Fatalf("ensure deliveries: %v", err)
	}
	for _, row := range rows {
		claimed, err := fixture.store.ClaimOrderDelivery(ctx, row.ID, now, now.Add(-5*time.Minute))
		if err != nil || !claimed {
			t.Fatalf("claim delivery = %t, %v; want true, nil", claimed, err)
		}
		if err := fixture.store.MarkOrderDeliverySent(ctx, row.ID, now); err != nil {
			t.Fatalf("mark delivery sent: %v", err)
		}
		if err := fixture.store.ActivateDeliveryAccess(ctx, order.ID, row.OrderItemID, now, now.AddDate(0, 0, 30)); err != nil {
			t.Fatalf("activate access: %v", err)
		}
	}
}

func TestAccessRevocationImpactBlocksOldCodeAndReleasesSlots(t *testing.T) {
	fixture := newCheckoutFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()

	orderA := paidProcessingOrder(t, fixture)
	deliverOrder(t, fixture, orderA, now)

	impact, err := fixture.store.RevocationImpact(ctx, orderA.ID)
	if err != nil {
		t.Fatalf("revocation impact: %v", err)
	}
	if len(impact) != 1 || impact[0].OtherActiveUsers != 0 {
		t.Fatalf("initial impact = %+v, want one account with no other users", impact)
	}

	// A second customer on the same shared account makes the shared-impact
	// warning real.
	resultB, err := fixture.checkout(t, fixture.userB, "revocation-shared-"+now.Format("150405.000000000"), "")
	if err != nil {
		t.Fatalf("checkout second customer: %v", err)
	}
	if _, err := fixture.store.TransitionOrderStatus(ctx, resultB.Order.ID, domain.OrderPaid, now); err != nil {
		t.Fatalf("mark second paid: %v", err)
	}
	orderB, err := fixture.store.TransitionOrderStatus(ctx, resultB.Order.ID, domain.OrderProcessing, now)
	if err != nil {
		t.Fatalf("mark second processing: %v", err)
	}
	deliverOrder(t, fixture, orderB, now)

	impact, err = fixture.store.RevocationImpact(ctx, orderA.ID)
	if err != nil {
		t.Fatalf("shared impact: %v", err)
	}
	if len(impact) != 1 || impact[0].OtherActiveUsers != 1 {
		t.Fatalf("shared impact = %+v, want one other active user", impact)
	}

	valid, err := fixture.store.ValidateAccessCode(ctx, orderA.ID, orderA.Items[0].ID, orderA.UserID, now)
	if err != nil || !valid {
		t.Fatalf("active code validation = %t, %v; want true, nil", valid, err)
	}

	if _, err := fixture.store.TransitionOrderStatus(ctx, orderA.ID, domain.OrderCancelled, now); err != nil {
		t.Fatalf("cancel order: %v", err)
	}
	if _, err := fixture.store.ReleaseOrderSlots(ctx, orderA.ID, now); err != nil {
		t.Fatalf("release slots: %v", err)
	}

	valid, err = fixture.store.ValidateAccessCode(ctx, orderA.ID, orderA.Items[0].ID, orderA.UserID, now)
	if err != nil || valid {
		t.Fatalf("revoked code validation = %t, %v; want false, nil", valid, err)
	}
	material, err := fixture.store.LoadDeliveryMaterial(ctx, orderA.ID, orderA.Items[0].ID)
	if err != nil {
		t.Fatalf("load material after release: %v", err)
	}
	if len(material.Slots) != 0 {
		t.Fatalf("material slots after release = %d, want 0", len(material.Slots))
	}
	impact, err = fixture.store.RevocationImpact(ctx, orderA.ID)
	if err != nil {
		t.Fatalf("impact after release: %v", err)
	}
	if len(impact) != 0 {
		t.Fatalf("impact after release = %+v, want empty", impact)
	}
}
