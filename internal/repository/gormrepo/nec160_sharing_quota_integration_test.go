package gormrepo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
)

// TestNEC160UsedUsersIsDistinctFromUsedSlots documents the dashboard contract:
// a repeat buyer may occupy several rows without consuming another unique-user
// seat, while a sixth distinct buyer is rejected.
func TestNEC160UsedUsersIsDistinctFromUsedSlots(t *testing.T) {
	fixture := newNEC97Fixture(t)
	ctx := context.Background()
	accountID := fixture.account(t, "sharing-usage", 5, nil)
	repeatUser := fixture.user(t, "repeat-user")

	_ = fixture.order(t, repeatUser, accountID, 1, domain.OrderProcessing)
	second := fixture.order(t, repeatUser, "", 1, domain.OrderProcessing)
	delivery := fixture.delivery(t, second.ID)
	if _, err := fixture.store.AssignDeliveryAccount(ctx, second.ID, delivery.ID, accountID, time.Now().UTC()); err != nil {
		t.Fatalf("repeat buyer assignment: %v", err)
	}

	// Inventory list: two occupied rows, one unique user.
	accounts, _, err := fixture.store.ListAccounts(ctx, 1, 20, fixture.productID, "")
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	var listed *domain.InventoryAccount
	for i := range accounts {
		if accounts[i].ID == accountID {
			listed = &accounts[i]
			break
		}
	}
	if listed == nil {
		t.Fatal("shared account not returned by inventory list")
	}
	if listed.UsedUsers != 1 || listed.AssignedUsers != 1 {
		t.Fatalf("used_users = %d, assigned_users = %d, want 1/1", listed.UsedUsers, listed.AssignedUsers)
	}
	if listed.UsedSlots != 2 {
		t.Fatalf("used_slots = %d, want 2 rows", listed.UsedSlots)
	}

	// Delivery options for the repeat buyer must not pretend the buyer is a new
	// user, and the row count must still be visible separately.
	third := fixture.order(t, repeatUser, "", 1, domain.OrderProcessing)
	options, err := fixture.store.LoadDeliveryOptions(ctx, third.ID)
	if err != nil {
		t.Fatalf("load delivery options: %v", err)
	}
	var option *service.DeliveryAccountOption
	for i := range options[0].Accounts {
		if options[0].Accounts[i].InventoryAccountID == accountID {
			option = &options[0].Accounts[i]
			break
		}
	}
	if option == nil {
		t.Fatal("shared account missing from delivery options")
	}
	if option.UsedUsers != 1 || option.AssignedUsers != 1 {
		t.Fatalf("option used_users = %d/%d, want 1/1", option.UsedUsers, option.AssignedUsers)
	}
	if option.UsedSlots != 2 {
		t.Fatalf("option used_slots = %d, want 2", option.UsedSlots)
	}
	if !option.CanAssign {
		t.Fatal("repeat buyer should be able to use another row on the same account")
	}

}

func TestNEC160SixthDistinctUserIsRejected(t *testing.T) {
	fixture := newNEC97Fixture(t)
	ctx := context.Background()
	accountID := fixture.account(t, "five-users", 5, nil)
	for i := 0; i < 5; i++ {
		userID := fixture.user(t, "member")
		fixture.order(t, userID, accountID, 1, domain.OrderProcessing)
	}

	sixthUser := fixture.user(t, "sixth")
	sixth := fixture.order(t, sixthUser, "", 1, domain.OrderProcessing)
	if _, err := fixture.store.AssignDeliveryAccount(ctx, sixth.ID, fixture.delivery(t, sixth.ID).ID, accountID, time.Now().UTC()); !errors.Is(err, service.ErrDeliveryAccountFull) {
		t.Fatalf("sixth user assignment error = %v, want ErrDeliveryAccountFull", err)
	}

	options, err := fixture.store.LoadDeliveryOptions(ctx, sixth.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range options[0].Accounts {
		if option.InventoryAccountID != accountID {
			continue
		}
		if option.UsedUsers != 5 || option.UsedSlots != 5 || option.CanAssign {
			t.Fatalf("full account option = %+v, want used_users=5 used_slots=5 can_assign=false", option)
		}
	}
}

func TestNEC160LegacyOverFiveUsersCannotReceiveAssignments(t *testing.T) {
	fixture := newNEC97Fixture(t)
	ctx := context.Background()
	legacy := fixture.account(t, "legacy-six", 5, nil)
	for i := 0; i < 6; i++ {
		userID := fixture.user(t, "legacy")
		fixture.order(t, userID, legacy, 1, domain.OrderProcessing)
	}

	newUser := fixture.user(t, "new-user")
	order := fixture.order(t, newUser, "", 1, domain.OrderProcessing)
	if _, err := fixture.store.AssignDeliveryAccount(ctx, order.ID, fixture.delivery(t, order.ID).ID, legacy, time.Now().UTC()); !errors.Is(err, service.ErrDeliveryAccountFull) {
		t.Fatalf("legacy assignment error = %v, want ErrDeliveryAccountFull", err)
	}

	// Automatic preparation must move the item off the over-cap account when an
	// eligible alternative exists.
	current := fixture.order(t, fixture.user(t, "current"), legacy, 1, domain.OrderProcessing)
	if err := fixture.store.PrepareDeliveryAssignment(ctx, current.ID, current.Items[0].ID, time.Now().UTC()); err != nil {
		t.Fatalf("prepare assignment: %v", err)
	}
	for _, assignedAccount := range fixture.slotAccounts(t, current.Items[0].ID) {
		if assignedAccount == legacy {
			t.Fatal("automatic preparation left the item on the legacy over-cap account")
		}
	}

	options, err := fixture.store.LoadDeliveryOptions(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range options[0].Accounts {
		if option.InventoryAccountID != legacy {
			continue
		}
		if option.UsedUsers != 6 || option.CanAssign {
			t.Fatalf("legacy option = %+v, want used_users=6 can_assign=false", option)
		}
	}
}
