package service

import "context"

// AuditWriter records security-relevant events so an operator can reconstruct
// who did what. The admin dashboard already has a viewer for these rows, but
// nothing ever wrote one, so the audit log stayed permanently empty.
type AuditWriter interface {
	Record(ctx context.Context, entry AuditEntry)
}

type AuditEntry struct {
	// ActorUserID is empty when the event cannot be tied to a known account, such
	// as a sign-in attempt with an address that does not exist.
	ActorUserID string
	Action      string
	Resource    string
	ResourceID  string
	IPAddress   string
	Metadata    map[string]any
}

// Action names emitted by the auth service.
const (
	AuditRegister           = "auth.register"
	AuditLoginSucceeded     = "auth.login.succeeded"
	AuditLoginFailed        = "auth.login.failed"
	AuditLoginBlocked       = "auth.login.blocked"
	AuditLogout             = "auth.logout"
	AuditEmailVerified      = "auth.email.verified"
	AuditVerificationResent = "auth.email.resent"
	AuditForgotPassword     = "auth.password.forgot"
	AuditPasswordReset      = "auth.password.reset"
	AuditPasswordChanged    = "auth.password.changed"
	AuditSessionsRevoked    = "auth.sessions.revoked"

	// Action names emitted by admin user management. They carry the actor, the
	// target account and the before/after value in the metadata column.
	AuditAdminUserProfileUpdated = "admin.user.profile_updated"
	AuditAdminUserRoleChanged    = "admin.user.role_changed"
	AuditAdminUserActivated      = "admin.user.activated"
	AuditAdminUserDeactivated    = "admin.user.deactivated"
	AuditAdminUserDeleted        = "admin.user.deleted"

	// Action names emitted by account delivery. Metadata carries the order,
	// order item, delivery id and error code only. Credentials and rendered
	// email bodies are never written to the audit log.
	AuditOrderDeliverySent     = "admin.order.delivery_sent"
	AuditOrderDeliveryFailed   = "admin.order.delivery_failed"
	AuditOrderDeliveryRetried  = "admin.order.delivery_retried"
	AuditOrderAccountAssigned  = "admin.order.delivery_account_assigned"
	AuditOrderDeliveryReplaced = "admin.order.delivery_replaced"

	// Action names emitted by inventory lifecycle management. Every change to an
	// account or slot expiry is recorded with the previous and new timestamps so
	// an operator can reconstruct why a buyer's access window moved.
	AuditInventoryAccountExtended    = "admin.inventory.account_extended"
	AuditInventoryAccountExpiry      = "admin.inventory.account_expiry_changed"
	AuditInventoryAccountStatus      = "admin.inventory.account_status_changed"
	AuditInventoryAccountDeactivated = "admin.inventory.account_deactivated"
	AuditInventorySlotExtended       = "admin.inventory.slot_extended"
	AuditInventorySlotDeactivated    = "admin.inventory.slot_deactivated"
	AuditInventoryAccountExpired     = "inventory.account.expired"
	AuditInventorySlotExpired        = "inventory.slot.expired"
)

// record is deliberately fire-and-forget: an audit write must never turn a
// successful sign-in into an error, and a nil writer simply disables recording.
func (s *AuthService) record(ctx context.Context, entry AuditEntry) {
	if s.audit == nil {
		return
	}
	s.audit.Record(ctx, entry)
}
