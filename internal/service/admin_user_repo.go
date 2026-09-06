package service

import (
	"context"
	"time"

	"github.com/necly/services/internal/domain"
)

type AdminUserRepository interface {
	ListUsers(ctx context.Context, filter UserFilter, page, perPage int) ([]domain.User, int64, error)
	GetUserByID(ctx context.Context, id string) (*domain.User, error)
	GetUserByEmail(ctx context.Context, email string) (*domain.User, error)
	CreateUserAdmin(ctx context.Context, u *domain.User) error
	UpdateUser(ctx context.Context, u *domain.User) error
	DeleteUser(ctx context.Context, id string, now time.Time) error
	UpdateUserRole(ctx context.Context, id, role string, now time.Time) error
	UpdateUserActive(ctx context.Context, id string, active bool, now time.Time) error
}

type UserFilter struct {
	Role     string
	Active   *bool
	Search   string
	DateFrom *time.Time
	DateTo   *time.Time
}
