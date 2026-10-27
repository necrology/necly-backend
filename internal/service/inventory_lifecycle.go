package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/necly/services/internal/domain"
)

// Inventory expiry lifecycle constants. The daily worker is deliberately
// bounded: it processes a few batches per run instead of holding one large
// transaction open. Running it again is always safe because every transition
// is guarded by the current status and expiry column.
const (
	InventoryExpiryBatchSize  = 200
	InventoryExpiryMaxBatches = 25
	InventoryExpiryInterval   = 24 * time.Hour
	InventoryExpiryTimeout    = time.Minute

	// Reminder windows are the two operator-facing checkpoints required by the
	// sharing-account plan.
	InventoryReminderH7Days = 7
	InventoryReminderH1Days = 1
)

// InventoryExpiryResult is the repository outcome of one bounded transition
// batch. The ids let the service write an audit row per changed entity without
// re-reading the rows.
type InventoryExpiryResult struct {
	SlotIDs    []string
	AccountIDs []string
}

// InventoryExpiryReminder is one slot or account that will expire inside a
// reminder window. It carries only presentation data; credentials never leave
// the repository through this path.
type InventoryExpiryReminder struct {
	TargetKind   string
	TargetID     string
	UserID       string
	ExpiresAt    time.Time
	ProductName  string
	AccountLabel string
}

// InventoryLifecycleResult summarises one worker run.
type InventoryLifecycleResult struct {
	SlotsExpired         int
	AccountsExpired      int
	NotificationsCreated int
}

// InventoryLifecycleService transits expired inventory rows and writes H-7 /
// H-1 reminders. It is the only component that performs automatic expiry; the
// admin actions live in AdminInventoryService so the HTTP contract stays close
// to the existing inventory routes.
type InventoryLifecycleService struct {
	repo      InventoryRepository
	audit     AuditWriter
	now       func() time.Time
	interval  time.Duration
	batchSize int
	timeout   time.Duration
}

func NewInventoryLifecycleService(repo InventoryRepository, audit AuditWriter) *InventoryLifecycleService {
	return &InventoryLifecycleService{
		repo:      repo,
		audit:     audit,
		now:       func() time.Time { return time.Now().UTC() },
		interval:  InventoryExpiryInterval,
		batchSize: InventoryExpiryBatchSize,
		timeout:   InventoryExpiryTimeout,
	}
}

// RunOnce executes one full lifecycle pass: transition expired rows, then emit
// reminders. It is exported so an operator command or test can run the exact
// same policy without a ticker.
func (s *InventoryLifecycleService) RunOnce(ctx context.Context) (InventoryLifecycleResult, error) {
	if s == nil || s.repo == nil {
		return InventoryLifecycleResult{}, nil
	}
	runCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	now := s.now().UTC().Truncate(time.Microsecond)
	result := InventoryLifecycleResult{}

	for batch := 0; batch < InventoryExpiryMaxBatches; batch++ {
		if runCtx.Err() != nil {
			return result, runCtx.Err()
		}
		expired, err := s.repo.ExpireInventory(runCtx, now, s.batchSize)
		if err != nil {
			return result, err
		}
		result.SlotsExpired += len(expired.SlotIDs)
		result.AccountsExpired += len(expired.AccountIDs)
		s.recordExpiryAudit(runCtx, now, expired)

		// A short batch means the tables no longer have candidates for this run.
		if len(expired.SlotIDs) < s.batchSize && len(expired.AccountIDs) < s.batchSize {
			break
		}
	}

	created, err := s.sendReminders(runCtx, now)
	if err != nil {
		return result, err
	}
	result.NotificationsCreated = created
	return result, nil
}

// Run is the daily background worker. It runs once at startup so a fresh
// deployment does not wait up to 24 hours to expire overdue rows.
func (s *InventoryLifecycleService) Run(ctx context.Context) {
	if s == nil || s.repo == nil {
		return
	}
	s.runBatch(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runBatch(ctx)
		}
	}
}

func (s *InventoryLifecycleService) runBatch(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	result, err := s.RunOnce(ctx)
	if err != nil {
		// The next tick retries. Every transition is idempotent, so an
		// interrupted batch leaves no partial state that needs manual cleanup.
		log.Printf("inventory lifecycle worker: %v", err)
		return
	}
	if result.SlotsExpired > 0 || result.AccountsExpired > 0 || result.NotificationsCreated > 0 {
		log.Printf("inventory lifecycle worker: expired_slots=%d expired_accounts=%d notifications=%d",
			result.SlotsExpired, result.AccountsExpired, result.NotificationsCreated)
	}
}

func (s *InventoryLifecycleService) sendReminders(ctx context.Context, now time.Time) (int, error) {
	windows := []int{InventoryReminderH7Days, InventoryReminderH1Days}
	notifications := make([]domain.Notification, 0)

	var adminIDs []string
	for _, windowDays := range windows {
		slots, err := s.repo.ListSlotExpiryReminders(ctx, now, windowDays)
		if err != nil {
			return 0, err
		}
		for _, reminder := range slots {
			notifications = append(notifications, slotReminderNotification(reminder, windowDays))
		}

		accounts, err := s.repo.ListAccountExpiryReminders(ctx, now, windowDays)
		if err != nil {
			return 0, err
		}
		if len(accounts) > 0 && adminIDs == nil {
			adminIDs, err = s.repo.ListActiveAdminIDs(ctx)
			if err != nil {
				return 0, err
			}
		}
		for _, reminder := range accounts {
			for _, adminID := range adminIDs {
				notifications = append(notifications, accountReminderNotification(reminder, adminID, windowDays))
			}
		}
	}
	if len(notifications) == 0 {
		return 0, nil
	}
	return s.repo.CreateNotifications(ctx, notifications)
}

func slotReminderNotification(reminder InventoryExpiryReminder, windowDays int) domain.Notification {
	label := reminder.ProductName
	if label == "" {
		label = "Langganan"
	}
	accountLabel := reminder.AccountLabel
	if accountLabel == "" {
		accountLabel = "Akun langganan"
	}
	key := expiryDedupeKey(reminder.TargetKind, reminder.TargetID, reminder.UserID, windowDays, reminder.ExpiresAt)
	return domain.Notification{
		UserID: reminder.UserID,
		Type:   "inventory_slot_expiry",
		Title:  fmt.Sprintf("Masa aktif %s tinggal %d hari", label, windowDays),
		Body: fmt.Sprintf("Masa aktif %s (%s) berakhir pada %s. Hubungi admin untuk memperpanjang sebelum akses berhenti.",
			label, accountLabel, formatExpiryDate(reminder.ExpiresAt)),
		DedupeKey: &key,
	}
}

func accountReminderNotification(reminder InventoryExpiryReminder, adminID string, windowDays int) domain.Notification {
	label := reminder.ProductName
	if label == "" {
		label = "Langganan"
	}
	accountLabel := reminder.AccountLabel
	if accountLabel == "" {
		accountLabel = "Akun inventaris"
	}
	key := expiryDedupeKey(reminder.TargetKind, reminder.TargetID, adminID, windowDays, reminder.ExpiresAt)
	return domain.Notification{
		UserID: adminID,
		Type:   "inventory_account_expiry",
		Title:  fmt.Sprintf("Akun inventaris %s berakhir dalam %d hari", label, windowDays),
		Body: fmt.Sprintf("Akun %s untuk %s berakhir pada %s. Perpanjang akun atau siapkan akun pengganti.",
			accountLabel, label, formatExpiryDate(reminder.ExpiresAt)),
		DedupeKey: &key,
	}
}

func expiryDedupeKey(kind, targetID, userID string, windowDays int, expiresAt time.Time) string {
	return fmt.Sprintf("inventory-%s:%s:%s:%dd:%s",
		kind, targetID, userID, windowDays, expiresAt.UTC().Format("20060102"))
}

func formatExpiryDate(value time.Time) string {
	return value.In(time.FixedZone("WIB", 7*3600)).Format("02 Jan 2006 15:04 WIB")
}

func (s *InventoryLifecycleService) recordExpiryAudit(ctx context.Context, now time.Time, result InventoryExpiryResult) {
	if s.audit == nil {
		return
	}
	for _, id := range result.SlotIDs {
		s.audit.Record(ctx, AuditEntry{
			Action:     AuditInventorySlotExpired,
			Resource:   "inventory_slot",
			ResourceID: id,
			Metadata: map[string]any{
				"source":     "scheduler",
				"changed_at": now.Format(time.RFC3339),
			},
		})
	}
	for _, id := range result.AccountIDs {
		s.audit.Record(ctx, AuditEntry{
			Action:     AuditInventoryAccountExpired,
			Resource:   "inventory_account",
			ResourceID: id,
			Metadata: map[string]any{
				"source":     "scheduler",
				"changed_at": now.Format(time.RFC3339),
			},
		})
	}
}

// NextSlotExpiry implements the perpanjangan rule:
//
//	access_expires_at = max(now, expiry lama) + durasi
//
// A nil or already-past expiry therefore starts from now instead of extending a
// window that has already lapsed.
func NextSlotExpiry(current *time.Time, now time.Time, durationDays int) time.Time {
	base := now.UTC()
	if current != nil && current.After(base) {
		base = current.UTC()
	}
	return base.AddDate(0, 0, durationDays)
}

// ValidateSlotExpiryWithinAccount rejects a slot window that would outlive the
// provider account. A nil account expiry means unlimited and always passes.
func ValidateSlotExpiryWithinAccount(slotExpiry time.Time, accountExpiry *time.Time) error {
	if accountExpiry == nil {
		return nil
	}
	if slotExpiry.After(accountExpiry.UTC()) {
		return ErrSlotExceedsAccountExpiry
	}
	return nil
}

// NextAccountExpiry extends from the later of now and the stored expiry. This
// is the same additive rule as slots, so renewing an account that is still
// active never shortens the remaining paid time.
func NextAccountExpiry(current *time.Time, now time.Time, durationDays int) time.Time {
	return NextSlotExpiry(current, now, durationDays)
}

// ValidateAccountExtensionTarget enforces that an explicit new expiry only
// moves forward. The admin action must never silently shorten a paid window.
func ValidateAccountExtensionTarget(current *time.Time, target, now time.Time) error {
	target = target.UTC()
	if !target.After(now.UTC()) {
		return ErrAccountExpiryNotExtended
	}
	if current != nil && !target.After(current.UTC()) {
		return ErrAccountExpiryNotExtended
	}
	return nil
}

// InventoryReminderWindow returns the half-open range used by one reminder pass. H-7
// catches anything between one and seven days away; H-1 catches the final day.
// The ranges do not overlap, so one run can never emit both reminders for the
// same expiry.
func InventoryReminderWindow(now time.Time, windowDays int) (after, upTo time.Time) {
	upTo = now.UTC().AddDate(0, 0, windowDays)
	if windowDays > InventoryReminderH1Days {
		after = now.UTC().AddDate(0, 0, InventoryReminderH1Days)
	} else {
		after = now.UTC()
	}
	return after, upTo
}
