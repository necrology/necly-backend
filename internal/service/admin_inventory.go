package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/platform/otpinbox"
)

const (
	InventoryStatusActive   = "active"
	InventoryStatusInactive = "inactive"
	InventoryStatusExpired  = "expired"

	// DefaultSharingLimit is deliberately conservative. A new account is not a
	// free-for-all seat pool; an administrator raises the limit explicitly.
	DefaultSharingLimit = 1
	// MaxSharingLimit is the product rule: one account serves at most five
	// users. Legacy accounts above this cap are not truncated automatically;
	// they are flagged needs_review and cannot add new slots until lowered.
	MaxSharingLimit = 5
	// MaxSlotsPerRequest matches the existing CreateSlots contract.
	MaxSlotsPerRequest = 100
	// MaxInventoryLabelLength matches inventory_accounts.label VARCHAR(120).
	MaxInventoryLabelLength = 120
	// DefaultMailboxIMAPHost is used when an account does not specify its own
	// mailbox host. Gmail is the common case for the accounts this store sells.
	DefaultMailboxIMAPHost = otpinbox.DefaultMailboxIMAPHost
	// maxAccountEmailLength is the RFC practical limit for an email address.
	maxAccountEmailLength = 254
	// maxMailboxIMAPHostLength keeps the host column bounded and rejects
	// accidental pasted URLs.
	maxMailboxIMAPHostLength = 255
	// MaxInventoryExtensionDays bounds an admin renewal. Ten years is well past
	// any realistic provider subscription while still rejecting typo-sized
	// values that would overflow the date arithmetic.
	MaxInventoryExtensionDays = 3650
)

type AdminInventoryService struct {
	repo   InventoryRepository
	cipher *CredentialCipher
	audit  AuditWriter
	now    func() time.Time
	otp    *otpRateLimiter
	// centralOTP is true when the shared OTP_IMAP_* mailbox is configured. It
	// only affects the derived otp_inbox_source field shown to the dashboard.
	centralOTP bool
}

func NewAdminInventoryService(repo InventoryRepository, cipher *CredentialCipher) *AdminInventoryService {
	now := func() time.Time { return time.Now().UTC() }
	return &AdminInventoryService{repo: repo, cipher: cipher, now: now, otp: newOTPRateLimiter(now)}
}

// SetAuditWriter wires the inventory lifecycle audit trail. It is optional so
// the many read-only tests can keep constructing the service without a store.
func (s *AdminInventoryService) SetAuditWriter(audit AuditWriter) {
	s.audit = audit
}

// SetCentralOTPInbox tells the admin API whether the shared OTP_IMAP_* mailbox
// exists. Without it the response would mark an account without its own app
// password as having no OTP inbox at all, even though the central fallback is
// available.
func (s *AdminInventoryService) SetCentralOTPInbox(ready bool) {
	s.centralOTP = ready
}

type InventoryAccountFilter struct {
	ProductID string
	// Status filters an exact lifecycle state: active, inactive, or expired.
	Status string
	// Active is kept for the pre-W15 query parameter. It is translated to an
	// exact status only when Status is empty.
	Active *bool
}

type CreateInventoryAccountInput struct {
	ProductID   string `json:"product_id"`
	Credentials string `json:"credentials"`
	// AccountEmail/AccountPassword are the separate login fields. They may be
	// sent instead of credentials; both are validated and stored in the same
	// encrypted credential payload the delivery flow already understands.
	AccountEmail       string     `json:"account_email"`
	AccountPassword    string     `json:"account_password"`
	MailboxAppPassword *string    `json:"mailbox_app_password"`
	MailboxIMAPHost    string     `json:"mailbox_imap_host"`
	Label              string     `json:"label"`
	SharingLimit       int        `json:"sharing_limit"`
	ExpiresAt          *time.Time `json:"expires_at"`
	// OTPSecret is write-only. It is accepted on create/update but never part
	// of a response; an empty value on update removes the stored secret.
	OTPSecret *string `json:"otp_secret"`
}

type UpdateInventoryAccountInput struct {
	Credentials        *string    `json:"credentials"`
	AccountEmail       *string    `json:"account_email"`
	AccountPassword    *string    `json:"account_password"`
	MailboxAppPassword *string    `json:"mailbox_app_password"`
	MailboxIMAPHost    *string    `json:"mailbox_imap_host"`
	Status             *string    `json:"status"`
	ExpiresAt          *time.Time `json:"expires_at"`
	Label              *string    `json:"label"`
	SharingLimit       *int       `json:"sharing_limit"`
	OTPSecret          *string    `json:"otp_secret"`
}

// ExtendInventoryAccountInput is the admin renewal contract. Exactly one of
// DurationDays and ExpiresAt must be provided:
//   - duration_days extends from max(now, current expiry)
//   - expires_at sets an explicit new expiry and must move forward
type ExtendInventoryAccountInput struct {
	DurationDays int        `json:"duration_days"`
	ExpiresAt    *time.Time `json:"expires_at"`
}

// ExtendInventorySlotInput always uses the additive formula required by the
// sharing-account plan: max(now, expiry lama) + duration_days.
type ExtendInventorySlotInput struct {
	DurationDays int `json:"duration_days"`
}

type CreateSlotsInput struct {
	ProductPriceID string `json:"product_price_id"`
	// InventoryAccountID is optional. When omitted an eligible active account
	// with spare capacity is selected automatically.
	InventoryAccountID string `json:"inventory_account_id"`
	Quantity           int    `json:"quantity"`
	// AccessExpiresAt is optional and only for provisioning a seat whose buyer
	// window is already known. It is validated against the account expiry; a
	// slot may never outlive the provider account.
	AccessExpiresAt *time.Time `json:"access_expires_at"`
}

// CreateSlotsResult reports where new capacity landed. The account id lets the
// dashboard refresh the right account when the caller did not choose one.
type CreateSlotsResult struct {
	Created            int    `json:"created"`
	InventoryAccountID string `json:"inventory_account_id"`
}

// CreateSlots menambah kapasitas jual untuk satu harga paket. Inilah satu-satunya
// jalan membuat baris inventory_slots: katalog menghitung ketersediaan dari baris
// berstatus available untuk product_price terkait, dan checkout mengunci baris
// yang sama. Setiap slot harus menunjuk satu akun inventaris, dan jumlahnya tidak
// boleh melebihi sharing_limit akun tersebut.
func (s *AdminInventoryService) CreateSlots(ctx context.Context, input CreateSlotsInput) (CreateSlotsResult, error) {
	if input.ProductPriceID == "" || input.Quantity < 1 || input.Quantity > MaxSlotsPerRequest {
		return CreateSlotsResult{}, ErrInvalidInput
	}

	productID, err := s.repo.GetPriceProductID(ctx, input.ProductPriceID)
	if err != nil {
		return CreateSlotsResult{}, err
	}

	slots := make([]domain.InventorySlot, 0, input.Quantity)
	for i := 0; i < input.Quantity; i++ {
		slots = append(slots, domain.InventorySlot{
			ProductPriceID:  input.ProductPriceID,
			Status:          "available",
			AccessExpiresAt: cloneTime(input.AccessExpiresAt),
		})
	}

	if input.InventoryAccountID != "" {
		if _, err := uuid.Parse(input.InventoryAccountID); err != nil {
			return CreateSlotsResult{}, ErrNotFound
		}
		if err := s.repo.CreateSlotsForAccount(ctx, input.InventoryAccountID, productID, slots); err != nil {
			return CreateSlotsResult{}, err
		}
		return CreateSlotsResult{Created: len(slots), InventoryAccountID: input.InventoryAccountID}, nil
	}

	accountID, err := s.repo.CreateSlotsAuto(ctx, productID, slots)
	if err != nil {
		return CreateSlotsResult{}, err
	}
	return CreateSlotsResult{Created: len(slots), InventoryAccountID: accountID}, nil
}

func (s *AdminInventoryService) ListInventoryAccounts(ctx context.Context, filter InventoryAccountFilter, page, perPage int) ([]domain.InventoryAccount, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}

	if filter.ProductID != "" {
		if _, err := uuid.Parse(filter.ProductID); err != nil {
			return nil, 0, ErrInvalidInput
		}
	}

	status := ""
	if filter.Status != "" {
		normalized, ok := normalizeInventoryStatus(filter.Status)
		if !ok {
			return nil, 0, ErrInvalidInput
		}
		status = normalized
	} else if filter.Active != nil {
		if *filter.Active {
			status = InventoryStatusActive
		} else {
			status = InventoryStatusInactive
		}
	}

	accounts, total, err := s.repo.ListAccounts(ctx, page, perPage, filter.ProductID, status)
	if err != nil {
		return nil, 0, err
	}
	for i := range accounts {
		s.hydrateAccountEmail(ctx, &accounts[i])
		s.presentAccount(&accounts[i])
	}
	return accounts, total, nil
}

func (s *AdminInventoryService) CreateInventoryAccount(ctx context.Context, input CreateInventoryAccountInput) (*domain.InventoryAccount, error) {
	if input.ProductID == "" {
		return nil, ErrInvalidInput
	}
	if _, err := uuid.Parse(input.ProductID); err != nil {
		return nil, ErrNotFound
	}

	label, err := createInventoryLabel(input.Label)
	if err != nil {
		return nil, err
	}
	sharingLimit, err := normalizeSharingLimit(input.SharingLimit)
	if err != nil {
		return nil, err
	}
	accountEmail, encrypted, err := s.buildCreateCredential(input)
	if err != nil {
		return nil, err
	}
	appPassword, err := s.sealMailboxAppPassword(input.MailboxAppPassword)
	if err != nil {
		return nil, err
	}
	imapHost, err := normalizeMailboxIMAPHost(input.MailboxIMAPHost)
	if err != nil {
		return nil, err
	}

	exists, err := s.repo.ProductExists(ctx, input.ProductID)
	if err != nil {
		return nil, fmt.Errorf("check inventory product: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}

	now := time.Now().UTC()
	otpSecret, err := s.sealOTPSecret(input.OTPSecret)
	if err != nil {
		return nil, err
	}
	account := &domain.InventoryAccount{
		ID:                          uuid.NewString(),
		ProductID:                   input.ProductID,
		Label:                       label,
		AccountEmail:                accountEmail,
		EncryptedCredentials:        encrypted,
		MailboxAppPasswordEncrypted: appPassword,
		MailboxIMAPHost:             imapHost,
		OTPSecretEncrypted:          otpSecret,
		Status:                      InventoryStatusActive,
		SharingLimit:                sharingLimit,
		ExpiresAt:                   input.ExpiresAt,
		CreatedAt:                   now,
		UpdatedAt:                   now,
	}

	if err := s.repo.CreateAccount(ctx, account); err != nil {
		return nil, fmt.Errorf("create inventory account: %w", err)
	}
	s.presentAccount(account)
	return account, nil
}

func (s *AdminInventoryService) UpdateInventoryAccount(ctx context.Context, id string, input UpdateInventoryAccountInput) (*domain.InventoryAccount, error) {
	return s.UpdateInventoryAccountAs(ctx, AdminActor{}, id, input)
}

// UpdateInventoryAccountAs is the audited form used by the admin route. The
// actor may be empty in tests, but every expiry or status change still becomes
// an audit row so the history is never silently lost.
func (s *AdminInventoryService) UpdateInventoryAccountAs(ctx context.Context, actor AdminActor, id string, input UpdateInventoryAccountInput) (*domain.InventoryAccount, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}

	// The update path is the only caller allowed to see the stored ciphertext;
	// without it an update that omits credentials would overwrite them with NULL.
	account, err := s.repo.GetAccountWithCredentials(ctx, id)
	if err != nil {
		return nil, err
	}
	previousStatus := account.Status
	previousExpiry := cloneTime(account.ExpiresAt)

	if _, err := s.mergeUpdateCredential(account, input); err != nil {
		return nil, err
	}
	if input.MailboxAppPassword != nil {
		appPassword, err := s.sealMailboxAppPassword(input.MailboxAppPassword)
		if err != nil {
			return nil, err
		}
		// nil keeps the old secret; an explicit empty value cleared it above.
		account.MailboxAppPasswordEncrypted = appPassword
	}
	if input.MailboxIMAPHost != nil {
		host, err := normalizeMailboxIMAPHost(*input.MailboxIMAPHost)
		if err != nil {
			return nil, err
		}
		account.MailboxIMAPHost = host
	}
	if input.Status != nil {
		status, ok := normalizeInventoryStatus(*input.Status)
		if !ok {
			return nil, ErrInvalidInput
		}
		account.Status = status
	}
	if input.Label != nil {
		label := strings.TrimSpace(*input.Label)
		if label == "" || utf8.RuneCountInString(label) > MaxInventoryLabelLength {
			return nil, ErrInvalidInput
		}
		account.Label = label
	}
	if input.SharingLimit != nil {
		// On update an explicit value is required to be a real cap. Zero is not
		// "use the default" here; it is an invalid limit that would otherwise
		// silently collapse the account to one seat.
		if *input.SharingLimit < 1 || *input.SharingLimit > MaxSharingLimit {
			return nil, ErrInvalidInput
		}
		sharingLimit := *input.SharingLimit
		// A limit below the provisioned slots would leave the account in an
		// inconsistent state: existing rows would exceed the new cap. Reject
		// instead of silently ignoring or deleting capacity.
		provisioned, err := s.repo.CountSlotsForAccount(ctx, id)
		if err != nil {
			return nil, err
		}
		if int64(sharingLimit) < provisioned {
			return nil, ErrSharingLimitExceeded
		}
		account.SharingLimit = sharingLimit
	}
	if input.OTPSecret != nil {
		if strings.TrimSpace(*input.OTPSecret) == "" {
			// Explicit empty string deletes the secret. An omitted field (nil)
			// leaves the existing secret untouched.
			account.OTPSecretEncrypted = nil
		} else {
			sealed, err := s.sealOTPSecret(input.OTPSecret)
			if err != nil {
				return nil, err
			}
			account.OTPSecretEncrypted = sealed
		}
	}
	if input.ExpiresAt != nil {
		account.ExpiresAt = input.ExpiresAt
	}

	account.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateAccount(ctx, account); err != nil {
		return nil, err
	}
	s.recordAccountUpdateAudit(ctx, actor, account, previousStatus, previousExpiry)
	s.presentAccount(account)
	return account, nil
}

// ExtendInventoryAccount implements the admin renewal action. It uses the same
// additive rule as slots when duration_days is supplied, and an explicit
// expires_at may only move the window forward.
func (s *AdminInventoryService) ExtendInventoryAccount(ctx context.Context, actor AdminActor, id string, input ExtendInventoryAccountInput) (*domain.InventoryAccount, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	if input.DurationDays < 0 || input.DurationDays > MaxInventoryExtensionDays {
		return nil, ErrInvalidInput
	}
	if (input.DurationDays == 0) == (input.ExpiresAt == nil) {
		// Both or neither would make the intent ambiguous.
		return nil, ErrInvalidInput
	}

	account, err := s.repo.GetAccountWithCredentials(ctx, id)
	if err != nil {
		return nil, err
	}
	now := s.now()
	previousStatus := account.Status
	previousExpiry := cloneTime(account.ExpiresAt)

	var target time.Time
	if input.DurationDays > 0 {
		target = NextAccountExpiry(account.ExpiresAt, now, input.DurationDays)
	} else {
		target = input.ExpiresAt.UTC()
		if err := ValidateAccountExtensionTarget(account.ExpiresAt, target, now); err != nil {
			return nil, err
		}
	}
	account.ExpiresAt = &target
	// Renewing an account that the daily job already marked expired puts it
	// back on sale. Inactive stays inactive: an operator deliberately hid it.
	if account.Status == InventoryStatusExpired {
		account.Status = InventoryStatusActive
	}
	if err := s.repo.UpdateAccount(ctx, account); err != nil {
		return nil, err
	}

	s.recordInventoryAudit(ctx, actor, AuditInventoryAccountExtended, "inventory_account", account.ID, map[string]any{
		"previous_status": previousStatus,
		"new_status":      account.Status,
		"previous_expiry": formatOptionalTime(previousExpiry),
		"new_expiry":      target.Format(time.RFC3339),
	})
	s.presentAccount(account)
	return account, nil
}

// DeactivateInventoryAccount is the explicit nonaktifkan action. It keeps the
// row and every order history entry intact and is idempotent for an account
// that is already inactive.
func (s *AdminInventoryService) DeactivateInventoryAccount(ctx context.Context, actor AdminActor, id string) (*domain.InventoryAccount, error) {
	status := InventoryStatusInactive
	return s.UpdateInventoryAccountAs(ctx, actor, id, UpdateInventoryAccountInput{Status: &status})
}

// ExtendInventorySlot applies max(now, expiry lama) + duration_days and rejects
// any result beyond the parent account's expiry.
func (s *AdminInventoryService) ExtendInventorySlot(ctx context.Context, actor AdminActor, slotID string, input ExtendInventorySlotInput) (*domain.InventorySlot, error) {
	if _, err := uuid.Parse(slotID); err != nil {
		return nil, ErrNotFound
	}
	if input.DurationDays < 1 || input.DurationDays > MaxInventoryExtensionDays {
		return nil, ErrInvalidInput
	}
	before, err := s.repo.GetSlot(ctx, slotID)
	if err != nil {
		return nil, err
	}
	previousExpiry := cloneTime(before.AccessExpiresAt)
	previousStatus := before.Status

	slot, err := s.repo.ExtendSlotAccess(ctx, slotID, s.now(), input.DurationDays)
	if err != nil {
		return nil, err
	}

	s.recordInventoryAudit(ctx, actor, AuditInventorySlotExtended, "inventory_slot", slot.ID, map[string]any{
		"inventory_account_id": slot.InventoryAccountID,
		"previous_status":      previousStatus,
		"new_status":           slot.Status,
		"previous_expiry":      formatOptionalTime(previousExpiry),
		"new_expiry":           formatOptionalTime(slot.AccessExpiresAt),
	})
	return slot, nil
}

// DeactivateInventorySlot removes one seat from sale without deleting its slot
// row or its historical order linkage.
func (s *AdminInventoryService) DeactivateInventorySlot(ctx context.Context, actor AdminActor, slotID string) (*domain.InventorySlot, error) {
	if _, err := uuid.Parse(slotID); err != nil {
		return nil, ErrNotFound
	}
	slot, err := s.repo.DeactivateSlot(ctx, slotID, s.now())
	if err != nil {
		return nil, err
	}
	s.recordInventoryAudit(ctx, actor, AuditInventorySlotDeactivated, "inventory_slot", slot.ID, map[string]any{
		"inventory_account_id": slot.InventoryAccountID,
		"new_status":           slot.Status,
	})
	return slot, nil
}

func (s *AdminInventoryService) recordAccountUpdateAudit(ctx context.Context, actor AdminActor, account *domain.InventoryAccount, previousStatus string, previousExpiry *time.Time) {
	if previousStatus != account.Status {
		action := AuditInventoryAccountStatus
		if account.Status == InventoryStatusInactive {
			action = AuditInventoryAccountDeactivated
		}
		s.recordInventoryAudit(ctx, actor, action, "inventory_account", account.ID, map[string]any{
			"previous_status": previousStatus,
			"new_status":      account.Status,
		})
	}
	if !sameOptionalTime(previousExpiry, account.ExpiresAt) {
		s.recordInventoryAudit(ctx, actor, AuditInventoryAccountExpiry, "inventory_account", account.ID, map[string]any{
			"previous_expiry": formatOptionalTime(previousExpiry),
			"new_expiry":      formatOptionalTime(account.ExpiresAt),
		})
	}
}

func (s *AdminInventoryService) recordInventoryAudit(ctx context.Context, actor AdminActor, action, resource, resourceID string, metadata map[string]any) {
	if s.audit == nil {
		return
	}
	s.audit.Record(ctx, AuditEntry{
		ActorUserID: actor.UserID,
		Action:      action,
		Resource:    resource,
		ResourceID:  resourceID,
		IPAddress:   actor.IPAddress,
		Metadata:    metadata,
	})
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := value.UTC()
	return &copied
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func (s *AdminInventoryService) GetInventoryAccount(ctx context.Context, id string) (*domain.InventoryAccount, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	account, err := s.repo.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	s.hydrateAccountEmail(ctx, account)
	s.presentAccount(account)
	return account, nil
}

func (s *AdminInventoryService) DeleteInventoryAccount(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	return s.repo.DeleteAccount(ctx, id)
}

// ListSlots returns the read-only slot and assignment view for one account. The
// account is loaded first so an unknown id is a clean 404 rather than an empty
// page that looks like an account with no capacity.
func (s *AdminInventoryService) ListSlots(ctx context.Context, accountID, productPriceID, status string, page, perPage int) ([]domain.InventorySlotAssignment, int64, error) {
	if _, err := uuid.Parse(accountID); err != nil {
		return nil, 0, ErrNotFound
	}
	if productPriceID != "" {
		if _, err := uuid.Parse(productPriceID); err != nil {
			return nil, 0, ErrInvalidInput
		}
	}
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 50
	}
	if _, err := s.repo.GetAccount(ctx, accountID); err != nil {
		return nil, 0, err
	}
	slots, total, err := s.repo.ListSlotAssignments(ctx, accountID, productPriceID, status, page, perPage)
	if err != nil {
		return nil, 0, err
	}
	now := s.now()
	for i := range slots {
		slots[i].DaysRemaining = domain.DaysRemaining(slots[i].AccessExpiresAt, now)
	}
	return slots, total, nil
}

// presentAccount normalizes the derived fields every account response carries
// and strips both write-only secrets. The repository already computes the
// counters; this is the last line of defence so a handler cannot serialize a
// ciphertext, and it recomputes time-dependent fields with the service clock.
func (s *AdminInventoryService) presentAccount(account *domain.InventoryAccount) {
	if len(account.OTPSecretEncrypted) > 0 {
		account.OTPConfigured = true
	}
	account.NeedsReview = account.SharingLimit > MaxSharingLimit || account.TotalSlots > MaxSharingLimit
	account.DaysRemaining = domain.DaysRemaining(account.ExpiresAt, s.now())
	direct := account.HasMailboxAppPassword || len(account.MailboxAppPasswordEncrypted) > 0
	switch {
	case direct:
		account.OTPInboxConfigured = true
		account.OTPInboxSource = "direct"
	case s.centralOTP:
		account.OTPInboxConfigured = true
		account.OTPInboxSource = "central"
	default:
		account.OTPInboxConfigured = false
		account.OTPInboxSource = "none"
	}
	if strings.TrimSpace(account.MailboxIMAPHost) == "" {
		account.MailboxIMAPHost = DefaultMailboxIMAPHost
	}
	account.EncryptedCredentials = nil
	account.OTPSecretEncrypted = nil
	account.MailboxAppPasswordEncrypted = nil
}

// inventoryLoginPayload is the JSON credential shape already understood by
// parseInventoryCredential and the delivery email builder.
type inventoryLoginPayload struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	Instructions string `json:"instructions,omitempty"`
}

func (s *AdminInventoryService) buildCreateCredential(input CreateInventoryAccountInput) (string, []byte, error) {
	explicitEmail := strings.TrimSpace(input.AccountEmail)
	email, err := normalizeAccountEmail(explicitEmail)
	if err != nil {
		return "", nil, err
	}
	explicitPassword := input.AccountPassword
	if explicitPassword != "" && strings.TrimSpace(explicitPassword) == "" {
		return "", nil, ErrInvalidInput
	}

	legacy := input.Credentials
	if strings.TrimSpace(legacy) == "" {
		if email == "" || explicitPassword == "" {
			return "", nil, ErrInvalidInput
		}
		sealed, err := s.sealLoginCredential(inventoryLoginPayload{Username: email, Password: explicitPassword})
		return email, sealed, err
	}
	if email == "" && explicitPassword == "" {
		// Preserve the pre-NEC-121 behavior for clients that still send only
		// credentials: encrypt the value verbatim, then derive the indexed email
		// when the username is a valid address.
		if s.cipher == nil {
			return "", nil, errors.New("credential cipher not configured")
		}
		sealed, err := s.cipher.Encrypt([]byte(legacy))
		if err != nil {
			return "", nil, fmt.Errorf("encrypt credentials: %w", err)
		}
		if username, _, _, ok := parseInventoryCredential([]byte(legacy)); ok {
			if normalized, err := normalizeAccountEmail(username); err == nil {
				email = normalized
			}
		}
		return email, sealed, nil
	}

	username, password, instructions, parsed := parseInventoryCredential([]byte(legacy))
	if !parsed {
		if email == "" || explicitPassword == "" {
			return "", nil, ErrInvalidInput
		}
		username, password = email, explicitPassword
	} else {
		if email == "" {
			// Legacy usernames are frequently not email addresses; only a valid
			// address is copied into the dedicated column.
			if normalized, err := normalizeAccountEmail(username); err == nil {
				email = normalized
			}
		}
		if explicitPassword != "" {
			password = explicitPassword
		}
		if email != "" {
			username = email
		}
	}
	if username == "" || password == "" {
		return "", nil, ErrInvalidInput
	}
	sealed, err := s.sealLoginCredential(inventoryLoginPayload{
		Username: username, Password: password, Instructions: instructions,
	})
	return email, sealed, err
}

// mergeUpdateCredential applies the tri-state PATCH contract: an omitted field
// preserves the stored value, while an explicit value replaces it. It returns
// true when the encrypted credential blob changed.
func (s *AdminInventoryService) mergeUpdateCredential(account *domain.InventoryAccount, input UpdateInventoryAccountInput) (bool, error) {
	credentialsSent := input.Credentials != nil
	emailSent := input.AccountEmail != nil
	passwordSent := input.AccountPassword != nil
	if !credentialsSent && !emailSent && !passwordSent {
		return false, nil
	}
	if credentialsSent && *input.Credentials == "" {
		return false, ErrInvalidInput
	}
	var explicitEmail string
	if emailSent {
		if strings.TrimSpace(*input.AccountEmail) == "" {
			return false, ErrInvalidInput
		}
		normalized, err := normalizeAccountEmail(*input.AccountEmail)
		if err != nil {
			return false, err
		}
		explicitEmail = normalized
	}
	if passwordSent && strings.TrimSpace(*input.AccountPassword) == "" {
		return false, ErrInvalidInput
	}
	if s.cipher == nil {
		return false, errors.New("credential cipher not configured")
	}

	// Legacy clients that only send credentials keep the old behavior: the
	// value is encrypted as-is. We still try to derive account_email from a
	// parseable username so OTP lookup can use the indexed column.
	if credentialsSent && !emailSent && !passwordSent {
		sealed, err := s.cipher.Encrypt([]byte(*input.Credentials))
		if err != nil {
			return false, fmt.Errorf("encrypt credentials: %w", err)
		}
		account.EncryptedCredentials = sealed
		if username, _, _, ok := parseInventoryCredential([]byte(*input.Credentials)); ok {
			if normalized, err := normalizeAccountEmail(username); err == nil {
				account.AccountEmail = normalized
				return true, nil
			}
		}
		return true, nil
	}

	var base []byte
	if credentialsSent {
		base = []byte(*input.Credentials)
	} else {
		if len(account.EncryptedCredentials) == 0 || s.cipher == nil {
			base = nil
		} else {
			plain, err := s.cipher.Decrypt(account.EncryptedCredentials)
			if err != nil {
				base = nil
			} else {
				base = plain
			}
		}
	}
	username, password, instructions, parsed := parseInventoryCredential(base)
	if !parsed {
		// A malformed legacy blob can only be repaired when the caller supplies
		// both halves of the login. Supplying one half would silently drop the
		// other and make the delivered credential unusable.
		if !(emailSent && passwordSent) {
			return false, ErrInvalidInput
		}
		username, password, instructions = "", "", ""
	}
	if emailSent {
		username = explicitEmail
		account.AccountEmail = explicitEmail
	} else if normalized, err := normalizeAccountEmail(username); err == nil {
		// Keep the column in sync with a username that is an email address.
		if normalized != "" {
			account.AccountEmail = normalized
		}
	}
	if passwordSent {
		password = *input.AccountPassword
	}
	if username == "" || password == "" {
		return false, ErrInvalidInput
	}
	sealed, err := s.sealLoginCredential(inventoryLoginPayload{
		Username: username, Password: password, Instructions: instructions,
	})
	if err != nil {
		return false, err
	}
	account.EncryptedCredentials = sealed
	return true, nil
}

func (s *AdminInventoryService) sealLoginCredential(payload inventoryLoginPayload) ([]byte, error) {
	if s.cipher == nil {
		return nil, errors.New("credential cipher not configured")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode credentials: %w", err)
	}
	sealed, err := s.cipher.Encrypt(encoded)
	if err != nil {
		return nil, fmt.Errorf("encrypt credentials: %w", err)
	}
	return sealed, nil
}

func (s *AdminInventoryService) sealMailboxAppPassword(value *string) ([]byte, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	if s.cipher == nil {
		return nil, errors.New("credential cipher not configured")
	}
	sealed, err := s.cipher.Encrypt([]byte(*value))
	if err != nil {
		return nil, fmt.Errorf("encrypt mailbox app password: %w", err)
	}
	return sealed, nil
}

func normalizeAccountEmail(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > maxAccountEmailLength {
		return "", ErrInvalidInput
	}
	addr, err := mail.ParseAddress(value)
	if err != nil || !strings.Contains(addr.Address, "@") || !strings.EqualFold(addr.Address, value) {
		return "", ErrInvalidInput
	}
	return value, nil
}

func normalizeMailboxIMAPHost(value string) (string, error) {
	host, err := otpinbox.NormalizeMailboxIMAPHost(value)
	if err != nil {
		return "", ErrInvalidInput
	}
	return host, nil
}

// hydrateAccountEmail backfills the dedicated email column for accounts created
// before NEC-121. Decryption failures and duplicate-email conflicts are ignored:
// one broken legacy row must not make the inventory list unavailable.
func (s *AdminInventoryService) hydrateAccountEmail(ctx context.Context, account *domain.InventoryAccount) {
	if strings.TrimSpace(account.AccountEmail) != "" || s.cipher == nil {
		return
	}
	encrypted := account.EncryptedCredentials
	if len(encrypted) == 0 {
		// The repository deliberately strips ciphertext from normal reads. For
		// a legacy row we load just that column, then decrypt in memory.
		if loader, ok := s.repo.(interface {
			GetAccountEncryptedCredentials(context.Context, string) ([]byte, error)
		}); ok {
			loaded, err := loader.GetAccountEncryptedCredentials(ctx, account.ID)
			if err != nil {
				return
			}
			encrypted = loaded
		}
	}
	if len(encrypted) == 0 {
		return
	}
	plain, err := s.cipher.Decrypt(encrypted)
	if err != nil {
		return
	}
	username, _, _, ok := parseInventoryCredential(plain)
	if !ok {
		return
	}
	email, err := normalizeAccountEmail(username)
	if err != nil || email == "" {
		return
	}
	account.AccountEmail = email
	if writer, ok := s.repo.(interface {
		BackfillAccountEmail(context.Context, string, string) error
	}); ok {
		_ = writer.BackfillAccountEmail(ctx, account.ID, email)
	}
}

func (s *AdminInventoryService) sealOTPSecret(value *string) ([]byte, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	canonical, err := normalizeOTPSecret(*value)
	if err != nil {
		return nil, err
	}
	if s.cipher == nil {
		return nil, errors.New("credential cipher not configured")
	}
	sealed, err := s.cipher.Encrypt([]byte(canonical))
	if err != nil {
		// The encryption error is about the cipher, never the secret value.
		return nil, fmt.Errorf("encrypt otp secret: %w", err)
	}
	return sealed, nil
}

// GenerateOTP returns the current six-digit TOTP code for one account. The
// caller must be an authenticated admin; the rate key additionally scopes the
// budget to this account and actor.
func (s *AdminInventoryService) GenerateOTP(ctx context.Context, accountID, actorID string) (*OTPCode, error) {
	if _, err := uuid.Parse(accountID); err != nil {
		return nil, ErrNotFound
	}
	if !s.otp.allow(accountID + "|" + actorID) {
		return nil, ErrOTPRateLimited
	}
	account, err := s.repo.GetAccountWithCredentials(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if len(account.OTPSecretEncrypted) == 0 {
		return nil, ErrOTPNotConfigured
	}
	if s.cipher == nil {
		return nil, errors.New("credential cipher not configured")
	}
	plain, err := s.cipher.Decrypt(account.OTPSecretEncrypted)
	if err != nil {
		return nil, fmt.Errorf("decrypt otp secret: %w", err)
	}
	code, err := GenerateTOTP(string(plain), s.now())
	if err != nil {
		return nil, fmt.Errorf("generate otp: %w", err)
	}
	return &code, nil
}

func createInventoryLabel(value string) (string, error) {
	label := strings.TrimSpace(value)
	if label == "" {
		label = "Akun inventaris"
	}
	if utf8.RuneCountInString(label) > MaxInventoryLabelLength {
		return "", ErrInvalidInput
	}
	return label, nil
}

func normalizeSharingLimit(value int) (int, error) {
	if value == 0 {
		return DefaultSharingLimit, nil
	}
	if value < 1 || value > MaxSharingLimit {
		return 0, ErrInvalidInput
	}
	return value, nil
}

func normalizeInventoryStatus(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case InventoryStatusActive:
		return InventoryStatusActive, true
	case InventoryStatusInactive:
		return InventoryStatusInactive, true
	case InventoryStatusExpired:
		return InventoryStatusExpired, true
	default:
		return "", false
	}
}
