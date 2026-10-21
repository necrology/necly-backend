package gormrepo

import (
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
)

func TestApplyAccountDerivedAggregatesAssignedUsersAndReviewFlag(t *testing.T) {
	now := time.Now().UTC()
	expiry := now.Add(3 * 24 * time.Hour)
	account := domain.InventoryAccount{
		Status:       "active",
		SharingLimit: 5,
		ExpiresAt:    &expiry,
	}

	applyAccountDerived(&account, 4, 2, 3, now)

	if account.TotalSlots != 4 || account.AvailableSlots != 2 || account.UsedSlots != 2 {
		t.Fatalf("slot counters = total %d available %d used %d, want 4/2/2", account.TotalSlots, account.AvailableSlots, account.UsedSlots)
	}
	if account.AssignedUsers != 3 {
		t.Fatalf("assigned_users = %d, want 3", account.AssignedUsers)
	}
	if account.RemainingSlots != 1 {
		t.Fatalf("remaining_slots = %d, want 1", account.RemainingSlots)
	}
	if account.DaysRemaining == nil || *account.DaysRemaining != 3 {
		t.Fatalf("days_remaining = %v, want 3", account.DaysRemaining)
	}
	if account.NeedsReview {
		t.Fatal("an account at or below five users must not need review")
	}
	if !account.Sellable {
		t.Fatal("active, unexpired account should be sellable")
	}
}

func TestApplyAccountDerivedFlagsLegacyLimitAboveFive(t *testing.T) {
	now := time.Now().UTC()
	account := domain.InventoryAccount{Status: "active", SharingLimit: 10}

	applyAccountDerived(&account, 6, 1, 5, now)

	if !account.NeedsReview {
		t.Fatal("legacy sharing_limit above five must be flagged needs_review")
	}
	if account.RemainingSlots != 4 {
		t.Fatalf("remaining_slots = %d, want 4", account.RemainingSlots)
	}
	if service.MaxSharingLimit != 5 {
		t.Fatalf("MaxSharingLimit = %d, want 5", service.MaxSharingLimit)
	}
}
