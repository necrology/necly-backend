package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
)

func TestNextSlotExpiryUsesTheLaterOfNowAndOldExpiry(t *testing.T) {
	now := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	future := now.AddDate(0, 0, 10)
	past := now.AddDate(0, 0, -3)

	if got := NextSlotExpiry(&future, now, 30); !got.Equal(future.AddDate(0, 0, 30)) {
		t.Fatalf("future expiry = %s, want %s", got, future.AddDate(0, 0, 30))
	}
	if got := NextSlotExpiry(&past, now, 30); !got.Equal(now.AddDate(0, 0, 30)) {
		t.Fatalf("past expiry = %s, want extension from now", got)
	}
	if got := NextSlotExpiry(nil, now, 7); !got.Equal(now.AddDate(0, 0, 7)) {
		t.Fatalf("nil expiry = %s, want now + 7d", got)
	}
}

func TestValidateSlotExpiryWithinAccountRejectsOverrun(t *testing.T) {
	now := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	accountExpiry := now.AddDate(0, 0, 30)

	if err := ValidateSlotExpiryWithinAccount(accountExpiry, &accountExpiry); err != nil {
		t.Fatalf("equal expiry rejected: %v", err)
	}
	if err := ValidateSlotExpiryWithinAccount(accountExpiry.Add(time.Second), &accountExpiry); !errors.Is(err, ErrSlotExceedsAccountExpiry) {
		t.Fatalf("overrun error = %v, want ErrSlotExceedsAccountExpiry", err)
	}
	if err := ValidateSlotExpiryWithinAccount(accountExpiry.AddDate(1, 0, 0), nil); err != nil {
		t.Fatalf("unlimited account rejected: %v", err)
	}
}

func TestValidateAccountExtensionTargetOnlyMovesForward(t *testing.T) {
	now := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	current := now.AddDate(0, 0, 10)

	if err := ValidateAccountExtensionTarget(&current, current.AddDate(0, 0, 30), now); err != nil {
		t.Fatalf("forward extension rejected: %v", err)
	}
	if err := ValidateAccountExtensionTarget(&current, current.Add(-time.Second), now); !errors.Is(err, ErrAccountExpiryNotExtended) {
		t.Fatalf("shorter target error = %v", err)
	}
	if err := ValidateAccountExtensionTarget(nil, now.Add(-time.Second), now); !errors.Is(err, ErrAccountExpiryNotExtended) {
		t.Fatalf("past target error = %v", err)
	}
}

func TestReminderWindowsDoNotOverlap(t *testing.T) {
	now := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	h7After, h7UpTo := InventoryReminderWindow(now, InventoryReminderH7Days)
	h1After, h1UpTo := InventoryReminderWindow(now, InventoryReminderH1Days)

	if !h7UpTo.Equal(now.AddDate(0, 0, 7)) || !h7After.Equal(now.AddDate(0, 0, 1)) {
		t.Fatalf("H-7 window = (%s, %s]", h7After, h7UpTo)
	}
	if !h1UpTo.Equal(now.AddDate(0, 0, 1)) || !h1After.Equal(now) {
		t.Fatalf("H-1 window = (%s, %s]", h1After, h1UpTo)
	}
	if h7After.Before(h1UpTo) {
		t.Fatalf("windows overlap: H-7 starts %s, H-1 ends %s", h7After, h1UpTo)
	}
}

func TestExtendInventoryAccountUsesAdditiveRuleAndWritesAudit(t *testing.T) {
	now := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	current := now.AddDate(0, 0, 10)
	id := uuid.NewString()
	repo := &stubInventoryRepo{
		accountWithCredentials: &domain.InventoryAccount{ID: id, Status: InventoryStatusActive, ExpiresAt: &current, SharingLimit: 5},
	}
	svc := NewAdminInventoryService(repo, nil)
	svc.now = func() time.Time { return now }
	audit := &fakeAudit{}
	svc.SetAuditWriter(audit)

	account, err := svc.ExtendInventoryAccount(context.Background(), AdminActor{UserID: "admin-1", IPAddress: "127.0.0.1"}, id, ExtendInventoryAccountInput{DurationDays: 30})
	if err != nil {
		t.Fatalf("ExtendInventoryAccount: %v", err)
	}
	want := current.AddDate(0, 0, 30)
	if account.ExpiresAt == nil || !account.ExpiresAt.Equal(want) {
		t.Fatalf("expiry = %v, want %s", account.ExpiresAt, want)
	}
	if !audit.has(AuditInventoryAccountExtended) {
		t.Fatalf("audit actions = %v, want %s", audit.actions(), AuditInventoryAccountExtended)
	}
	if repo.updatedAccount == nil {
		t.Fatal("account was not persisted")
	}
}

func TestExtendInventoryAccountRejectsAmbiguousOrBackwardInput(t *testing.T) {
	now := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	current := now.AddDate(0, 0, 10)
	id := uuid.NewString()
	repo := &stubInventoryRepo{
		accountWithCredentials: &domain.InventoryAccount{ID: id, Status: InventoryStatusActive, ExpiresAt: &current, SharingLimit: 5},
	}
	svc := NewAdminInventoryService(repo, nil)
	svc.now = func() time.Time { return now }

	if _, err := svc.ExtendInventoryAccount(context.Background(), AdminActor{}, id, ExtendInventoryAccountInput{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty input error = %v, want ErrInvalidInput", err)
	}
	both := ExtendInventoryAccountInput{DurationDays: 30, ExpiresAt: &current}
	if _, err := svc.ExtendInventoryAccount(context.Background(), AdminActor{}, id, both); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ambiguous input error = %v, want ErrInvalidInput", err)
	}
	shorter := current.AddDate(0, 0, -1)
	if _, err := svc.ExtendInventoryAccount(context.Background(), AdminActor{}, id, ExtendInventoryAccountInput{ExpiresAt: &shorter}); !errors.Is(err, ErrAccountExpiryNotExtended) {
		t.Fatalf("backward target error = %v, want ErrAccountExpiryNotExtended", err)
	}
	if repo.updatedAccount != nil {
		t.Fatal("an invalid extension was persisted")
	}
}

func TestExtendInventorySlotReportsAccountLimitAndWritesAudit(t *testing.T) {
	now := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	oldExpiry := now.AddDate(0, 0, 2)
	newExpiry := now.AddDate(0, 0, 32)
	slotID := uuid.NewString()
	repo := &stubInventoryRepo{
		slot:     &domain.InventorySlot{ID: slotID, Status: InventoryStatusExpired, AccessExpiresAt: &oldExpiry},
		extended: &domain.InventorySlot{ID: slotID, InventoryAccountID: "acct-1", Status: "reserved", AccessExpiresAt: &newExpiry},
	}
	svc := NewAdminInventoryService(repo, nil)
	svc.now = func() time.Time { return now }
	audit := &fakeAudit{}
	svc.SetAuditWriter(audit)

	slot, err := svc.ExtendInventorySlot(context.Background(), AdminActor{UserID: "admin-1"}, slotID, ExtendInventorySlotInput{DurationDays: 30})
	if err != nil {
		t.Fatalf("ExtendInventorySlot: %v", err)
	}
	if slot.Status != "reserved" || !slot.AccessExpiresAt.Equal(newExpiry) {
		t.Fatalf("slot = %+v", slot)
	}
	if len(repo.extendDays) != 1 || repo.extendDays[0] != 30 {
		t.Fatalf("repo extension days = %v", repo.extendDays)
	}
	if !audit.has(AuditInventorySlotExtended) {
		t.Fatalf("audit actions = %v, want %s", audit.actions(), AuditInventorySlotExtended)
	}

	repo.extendErr = ErrSlotExceedsAccountExpiry
	if _, err := svc.ExtendInventorySlot(context.Background(), AdminActor{}, slotID, ExtendInventorySlotInput{DurationDays: 30}); !errors.Is(err, ErrSlotExceedsAccountExpiry) {
		t.Fatalf("overrun error = %v, want ErrSlotExceedsAccountExpiry", err)
	}
}

func TestInventoryLifecycleRunOnceIsIdempotent(t *testing.T) {
	now := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	slotExpiry := now.AddDate(0, 0, 7)
	accountExpiry := now.AddDate(0, 0, 1)
	repo := &stubInventoryRepo{
		expiry: InventoryExpiryResult{SlotIDs: []string{"slot-1"}, AccountIDs: []string{"acct-1"}},
		slotReminders: map[int][]InventoryExpiryReminder{
			InventoryReminderH7Days: {{TargetKind: "slot", TargetID: "slot-1", UserID: "user-1", ExpiresAt: slotExpiry, ProductName: "Netflix"}},
			InventoryReminderH1Days: {{TargetKind: "slot", TargetID: "slot-2", UserID: "user-2", ExpiresAt: accountExpiry, ProductName: "Spotify"}},
		},
		accountReminders: map[int][]InventoryExpiryReminder{
			InventoryReminderH7Days: {{TargetKind: "account", TargetID: "acct-9", ExpiresAt: slotExpiry, ProductName: "Disney+", AccountLabel: "Akun utama"}},
		},
		adminIDs: []string{"admin-1", "admin-2"},
	}
	svc := NewInventoryLifecycleService(repo, nil)
	svc.now = func() time.Time { return now }

	result, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.SlotsExpired != 1 || result.AccountsExpired != 1 {
		t.Fatalf("result = %+v", result)
	}
	// One H-7 slot, one H-1 slot, and one H-7 account reminder per admin.
	if result.NotificationsCreated != 4 || len(repo.createdNotifications) != 4 {
		t.Fatalf("notifications = %d (stored %d), want 4", result.NotificationsCreated, len(repo.createdNotifications))
	}
	seen := map[string]bool{}
	for _, notification := range repo.createdNotifications {
		if notification.DedupeKey == nil || *notification.DedupeKey == "" {
			t.Fatalf("notification without dedupe key: %+v", notification)
		}
		if seen[*notification.DedupeKey] {
			t.Fatalf("duplicate dedupe key %q", *notification.DedupeKey)
		}
		seen[*notification.DedupeKey] = true
	}

	second, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if second.NotificationsCreated != 0 || len(repo.createdNotifications) != 4 {
		t.Fatalf("second run created %d notifications, want 0", second.NotificationsCreated)
	}
}

func TestInventoryLifecycleRunOnceAuditsExpiry(t *testing.T) {
	now := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	repo := &stubInventoryRepo{
		expiry: InventoryExpiryResult{SlotIDs: []string{"slot-1"}, AccountIDs: []string{"acct-1"}},
	}
	audit := &fakeAudit{}
	svc := NewInventoryLifecycleService(repo, audit)
	svc.now = func() time.Time { return now }

	if _, err := svc.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !audit.has(AuditInventorySlotExpired) || !audit.has(AuditInventoryAccountExpired) {
		t.Fatalf("audit actions = %v, want slot and account expiry", audit.actions())
	}
}
