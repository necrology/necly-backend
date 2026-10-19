package service

import (
	"context"
	"time"

	"github.com/necly/services/internal/domain"
)

// AdminUserUpdate describes a partial update of an account. A nil field means
// "leave unchanged", which keeps the API patch semantics honest and prevents an
// omitted JSON key from silently resetting a value.
type AdminUserUpdate struct {
	Name   *string
	Email  *string
	Role   *domain.UserRole
	Active *bool
}

type AdminUserRepository interface {
	ListUsers(ctx context.Context, filter UserFilter, page, perPage int) ([]domain.User, int64, error)
	GetUserByID(ctx context.Context, id string) (*domain.User, error)
	GetUserByEmail(ctx context.Context, email string) (*domain.User, error)
	CreateUserAdmin(ctx context.Context, u *domain.User) error
	// CountActiveAdmins returns how many other active administrators exist,
	// excluding excludeUserID. It is the readable half of the last-admin guard.
	CountActiveAdmins(ctx context.Context, excludeUserID string) (int64, error)
	// ApplyUserUpdate persists the change and re-checks the last-admin rule in
	// the same transaction. The service also checks first so the common case
	// fails with a clear error, but this inner check is what makes the rule
	// true under concurrent requests.
	ApplyUserUpdate(ctx context.Context, id string, update AdminUserUpdate, now time.Time) (*domain.User, error)
	DeleteUser(ctx context.Context, id string, now time.Time) error
}

type UserFilter struct {
	Role     string
	Active   *bool
	Search   string
	DateFrom *time.Time
	DateTo   *time.Time
}
