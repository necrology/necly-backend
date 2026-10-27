package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
)

type revocationFakeRepo struct {
	order       *domain.Order
	impact      []RevocationAccountImpact
	transitions []domain.OrderStatus
	released    int
}

func (r *revocationFakeRepo) ListOrders(context.Context, OrderFilter, int, int) ([]domain.Order, int64, error) {
	return nil, 0, nil
}

func (r *revocationFakeRepo) GetOrderByID(_ context.Context, id string) (*domain.Order, error) {
	if r.order == nil || r.order.ID != id {
		return nil, ErrNotFound
	}
	copyOrder := *r.order
	return &copyOrder, nil
}

func (r *revocationFakeRepo) TransitionOrderStatus(_ context.Context, id string, status domain.OrderStatus, _ time.Time) (*domain.Order, error) {
	if r.order == nil || r.order.ID != id {
		return nil, ErrNotFound
	}
	r.transitions = append(r.transitions, status)
	r.order.Status = status
	copyOrder := *r.order
	return &copyOrder, nil
}

func (r *revocationFakeRepo) UpdateOrder(context.Context, *domain.Order) error { return nil }

func (r *revocationFakeRepo) RevocationImpact(context.Context, string) ([]RevocationAccountImpact, error) {
	return r.impact, nil
}

func (r *revocationFakeRepo) ReleaseOrderSlots(context.Context, string, time.Time) (int, error) {
	return r.released, nil
}

type revocationFakeRotator struct {
	rotated map[string]string
	err     error
}

func (r *revocationFakeRotator) RotateAccountPassword(_ context.Context, accountID, newPassword string) error {
	if r.err != nil {
		return r.err
	}
	if r.rotated == nil {
		r.rotated = map[string]string{}
	}
	r.rotated[accountID] = newPassword
	return nil
}

func revocationFixture(status domain.OrderStatus, impact ...RevocationAccountImpact) (*AdminOrderService, *revocationFakeRepo, *revocationFakeRotator) {
	repo := &revocationFakeRepo{
		order:  &domain.Order{ID: uuid.NewString(), UserID: uuid.NewString(), Status: status},
		impact: impact,
	}
	rotator := &revocationFakeRotator{}
	svc := NewAdminOrderService(repo)
	svc.SetCredentialRotator(rotator)
	return svc, repo, rotator
}

func TestRevokeAccessRequiresProviderRotationForDeliveredOrder(t *testing.T) {
	accountID := uuid.NewString()
	svc, repo, rotator := revocationFixture(domain.OrderDelivered, RevocationAccountImpact{
		InventoryAccountID: accountID,
	})

	result, err := svc.RevokeAccess(context.Background(), AdminActor{UserID: "admin"}, repo.order.ID, RevokeAccessInput{
		Status: domain.OrderCancelled,
		Reason: "Pelanggan berhenti berlangganan",
	})
	if !errors.Is(err, ErrProviderRotationRequired) {
		t.Fatalf("error = %v, want ErrProviderRotationRequired", err)
	}
	if result == nil || len(result.Accounts) != 1 {
		t.Fatalf("result must explain the impacted account: %+v", result)
	}
	if len(rotator.rotated) != 0 || len(repo.transitions) != 0 {
		t.Fatal("nothing may be rotated or transitioned before the operator confirms")
	}
}

func TestRevokeAccessRequiresSharedImpactAcknowledgement(t *testing.T) {
	accountID := uuid.NewString()
	svc, repo, _ := revocationFixture(domain.OrderDelivered, RevocationAccountImpact{
		InventoryAccountID: accountID, OtherActiveUsers: 2,
	})

	_, err := svc.RevokeAccess(context.Background(), AdminActor{UserID: "admin"}, repo.order.ID, RevokeAccessInput{
		Status:                    domain.OrderCancelled,
		Reason:                    "Pelanggan berhenti berlangganan",
		ProviderRotationConfirmed: true,
		NewPasswords:              map[string]string{accountID: "kata-sandi-baru"},
	})
	if !errors.Is(err, ErrSharedAccessImpact) {
		t.Fatalf("error = %v, want ErrSharedAccessImpact", err)
	}
}

func TestRevokeAccessRotatesPasswordThenCancelsAndReleasesSlots(t *testing.T) {
	accountID := uuid.NewString()
	svc, repo, rotator := revocationFixture(domain.OrderDelivered, RevocationAccountImpact{
		InventoryAccountID: accountID, OtherActiveUsers: 1,
	})
	repo.released = 2

	result, err := svc.RevokeAccess(context.Background(), AdminActor{UserID: "admin"}, repo.order.ID, RevokeAccessInput{
		Status:                    domain.OrderRefunded,
		Reason:                    "Refund disetujui",
		ProviderRotationConfirmed: true,
		SharedImpactAcknowledged:  true,
		NewPasswords:              map[string]string{accountID: "kata-sandi-baru"},
	})
	if err != nil {
		t.Fatalf("RevokeAccess: %v", err)
	}
	if rotator.rotated[accountID] != "kata-sandi-baru" {
		t.Fatalf("rotated password = %q", rotator.rotated[accountID])
	}
	if len(repo.transitions) != 1 || repo.transitions[0] != domain.OrderRefunded {
		t.Fatalf("transitions = %v", repo.transitions)
	}
	if result.ReleasedSlots != 2 {
		t.Fatalf("released slots = %d, want 2", result.ReleasedSlots)
	}
}

func TestRevokeAccessRequiresNewPasswordForEveryAccount(t *testing.T) {
	accountID := uuid.NewString()
	svc, repo, _ := revocationFixture(domain.OrderDelivered, RevocationAccountImpact{
		InventoryAccountID: accountID,
	})

	_, err := svc.RevokeAccess(context.Background(), AdminActor{UserID: "admin"}, repo.order.ID, RevokeAccessInput{
		Status:                    domain.OrderCancelled,
		Reason:                    "Pelanggan berhenti berlangganan",
		ProviderRotationConfirmed: true,
		NewPasswords:              map[string]string{accountID: "   "},
	})
	if !errors.Is(err, ErrNewPasswordRequired) {
		t.Fatalf("error = %v, want ErrNewPasswordRequired", err)
	}
}

func TestUpdateOrderStatusCannotCancelDeliveredOrderDirectly(t *testing.T) {
	svc, repo, _ := revocationFixture(domain.OrderDelivered, RevocationAccountImpact{
		InventoryAccountID: uuid.NewString(),
	})

	_, err := svc.UpdateOrderStatus(context.Background(), repo.order.ID, domain.OrderCancelled)
	if !errors.Is(err, ErrProviderRotationRequired) {
		t.Fatalf("error = %v, want ErrProviderRotationRequired", err)
	}
	if len(repo.transitions) != 0 {
		t.Fatal("plain status update must not transition a delivered order")
	}
}

func TestUpdateOrderStatusAllowsCancellationBeforeDelivery(t *testing.T) {
	svc, repo, _ := revocationFixture(domain.OrderPaid)

	if _, err := svc.UpdateOrderStatus(context.Background(), repo.order.ID, domain.OrderCancelled); err != nil {
		t.Fatalf("UpdateOrderStatus: %v", err)
	}
	if len(repo.transitions) != 1 || repo.transitions[0] != domain.OrderCancelled {
		t.Fatalf("transitions = %v", repo.transitions)
	}
}
