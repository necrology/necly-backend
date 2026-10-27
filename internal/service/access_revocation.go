package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
)

// AccountPasswordRotator updates the encrypted provider password stored on an
// inventory account. The operator changes the password on the platform first,
// then submits the new value here so the seat cannot be resold with the old
// credential.
type AccountPasswordRotator interface {
	RotateAccountPassword(ctx context.Context, accountID, newPassword string) error
}

// RevocationAccountImpact is the credential-free warning shown before a
// delivered subscription is cancelled. OtherActiveUsers counts distinct
// customers that still hold an active seat on the same inventory account.
type RevocationAccountImpact struct {
	InventoryAccountID string `json:"inventory_account_id"`
	Label              string `json:"label"`
	AccountEmail       string `json:"account_email,omitempty"`
	OtherActiveUsers   int    `json:"other_active_users"`
}

// RevokeAccessInput is the complete cancellation request. A delivered order
// requires ProviderRotationConfirmed and one new password per delivered account
// because removing the database slot alone cannot revoke a login that already
// happened outside the platform.
type RevokeAccessInput struct {
	Status                    domain.OrderStatus `json:"status"`
	Reason                    string             `json:"reason"`
	ProviderRotationConfirmed bool               `json:"provider_rotation_confirmed"`
	SharedImpactAcknowledged  bool               `json:"shared_impact_acknowledged"`
	NewPasswords              map[string]string  `json:"new_passwords"`
}

// RevokeAccessResult returns the updated order plus the accounts the operator
// had to rotate. The result is also returned with validation errors so the UI
// can explain exactly which accounts block the cancellation.
type RevokeAccessResult struct {
	Order         *domain.Order             `json:"order,omitempty"`
	Accounts      []RevocationAccountImpact `json:"accounts"`
	ReleasedSlots int                       `json:"released_slots"`
}

const maxRevocationReasonLength = 500

// RevocationImpact lists delivered accounts that still back the order. An empty
// list means no credential has left the platform yet, so cancellation only needs
// to release the reservation.
func (s *AdminOrderService) RevocationImpact(ctx context.Context, orderID string) ([]RevocationAccountImpact, error) {
	if _, err := uuid.Parse(orderID); err != nil {
		return nil, ErrNotFound
	}
	accounts, err := s.repo.RevocationImpact(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if accounts == nil {
		accounts = []RevocationAccountImpact{}
	}
	return accounts, nil
}

// RevokeAccess cancels or refunds an order and removes the buyer's access.
//
// For a delivered order the caller must confirm that the provider password was
// changed and supply the new password for every account in the impact report.
// Passwords are stored encrypted by the inventory service; they never appear in
// the response, audit log or error message.
func (s *AdminOrderService) RevokeAccess(ctx context.Context, actor AdminActor, orderID string, input RevokeAccessInput) (*RevokeAccessResult, error) {
	if _, err := uuid.Parse(orderID); err != nil {
		return nil, ErrNotFound
	}
	if input.Status != domain.OrderCancelled && input.Status != domain.OrderRefunded {
		return nil, ErrInvalidInput
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" || len(reason) > maxRevocationReasonLength {
		return nil, ErrInvalidInput
	}

	order, err := s.repo.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if !IsValidOrderTransition(order.Status, input.Status) {
		return nil, ErrInvalidTransition
	}

	impact, err := s.repo.RevocationImpact(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if impact == nil {
		impact = []RevocationAccountImpact{}
	}
	result := &RevokeAccessResult{Order: order, Accounts: impact}

	if len(impact) > 0 {
		if !input.ProviderRotationConfirmed {
			return result, ErrProviderRotationRequired
		}
		for _, account := range impact {
			if account.OtherActiveUsers > 0 && !input.SharedImpactAcknowledged {
				return result, ErrSharedAccessImpact
			}
		}
		if s.credentialRotator == nil {
			return result, ErrCredentialRotationUnavailable
		}
		for _, account := range impact {
			if strings.TrimSpace(input.NewPasswords[account.InventoryAccountID]) == "" {
				return result, ErrNewPasswordRequired
			}
		}

		// Rotate before cancelling. If this step fails the order is untouched and
		// the operator can retry; if it succeeds the old buyer password is dead
		// even when the status write below fails.
		for _, account := range impact {
			if err := s.credentialRotator.RotateAccountPassword(
				ctx, account.InventoryAccountID, strings.TrimSpace(input.NewPasswords[account.InventoryAccountID]),
			); err != nil {
				return result, fmt.Errorf("rotate account password: %w", err)
			}
		}
	}

	now := s.nowTime()
	updated, err := s.repo.TransitionOrderStatus(ctx, orderID, input.Status, now)
	if err != nil {
		return result, err
	}
	result.Order = updated

	released, err := s.repo.ReleaseOrderSlots(ctx, orderID, now)
	if err != nil {
		// Access is already blocked by the cancelled/refunded status. Leaking the
		// error is safe; the slot stays allocated rather than being resold.
		return result, fmt.Errorf("release order slots: %w", err)
	}
	result.ReleasedSlots = released

	if s.audit != nil {
		s.audit.Record(ctx, AuditEntry{
			ActorUserID: actor.UserID,
			Action:      AuditOrderAccessRevoked,
			Resource:    "order",
			ResourceID:  orderID,
			IPAddress:   actor.IPAddress,
			Metadata: map[string]any{
				"status":           string(input.Status),
				"reason":           reason,
				"accounts":         len(impact),
				"released_slots":   released,
				"rotated_accounts": len(impact),
			},
		})
	}
	return result, nil
}

func (s *AdminOrderService) nowTime() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}
