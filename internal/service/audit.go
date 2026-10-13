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
)

// record is deliberately fire-and-forget: an audit write must never turn a
// successful sign-in into an error, and a nil writer simply disables recording.
func (s *AuthService) record(ctx context.Context, entry AuditEntry) {
	if s.audit == nil {
		return
	}
	s.audit.Record(ctx, entry)
}
