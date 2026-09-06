package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
)

type AdminUserService struct {
	repo AdminUserRepository
}

func NewAdminUserService(repo AdminUserRepository) *AdminUserService {
	return &AdminUserService{repo: repo}
}

func (s *AdminUserService) ListUsers(ctx context.Context, filter UserFilter, page, perPage int) ([]domain.User, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	return s.repo.ListUsers(ctx, filter, page, perPage)
}

func (s *AdminUserService) GetUser(ctx context.Context, id string) (*domain.User, error) {
	return s.repo.GetUserByID(ctx, id)
}

func (s *AdminUserService) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	return s.repo.GetUserByEmail(ctx, email)
}

func (s *AdminUserService) CreateUser(ctx context.Context, email, name, passwordHash string, role domain.UserRole) (*domain.User, error) {
	if email == "" || passwordHash == "" {
		return nil, ErrInvalidInput
	}
	// Check if email already exists
	existing, err := s.repo.GetUserByEmail(ctx, email)
	if err == nil && existing != nil {
		return nil, ErrConflict
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("check existing user: %w", err)
	}

	now := time.Now().UTC()
	user := &domain.User{
		ID:              uuid.NewString(),
		Email:           strings.ToLower(email),
		Name:            name,
		PasswordHash:    passwordHash,
		Role:            role,
		Active:          true,
		EmailVerifiedAt: nil,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := s.repo.CreateUserAdmin(ctx, user); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	user.PasswordHash = ""
	return user, nil
}

func (s *AdminUserService) UpdateUser(ctx context.Context, id, name, email string, active *bool) (*domain.User, error) {
	user, err := s.repo.GetUserByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if name != "" {
		user.Name = name
	}
	if email != "" {
		// Check if new email is already taken
		existing, err := s.repo.GetUserByEmail(ctx, email)
		if err == nil && existing != nil && existing.ID != id {
			return nil, ErrConflict
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("check existing email: %w", err)
		}
		user.Email = strings.ToLower(email)
	}
	if active != nil {
		user.Active = *active
	}

	user.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("update user: %w", err)
	}
	user.PasswordHash = ""
	return user, nil
}

func (s *AdminUserService) UpdateUserRole(ctx context.Context, id string, role domain.UserRole) (*domain.User, error) {
	validRoles := map[domain.UserRole]bool{
		domain.RoleCustomer: true,
		domain.RoleAdmin:    true,
		domain.RoleSeller:   true,
	}
	if !validRoles[role] {
		return nil, ErrInvalidInput
	}

	now := time.Now().UTC()
	if err := s.repo.UpdateUserRole(ctx, id, string(role), now); err != nil {
		return nil, fmt.Errorf("update user role: %w", err)
	}
	return s.repo.GetUserByID(ctx, id)
}

func (s *AdminUserService) UpdateUserActive(ctx context.Context, id string, active bool) (*domain.User, error) {
	now := time.Now().UTC()
	if err := s.repo.UpdateUserActive(ctx, id, active, now); err != nil {
		return nil, fmt.Errorf("update user active: %w", err)
	}
	return s.repo.GetUserByID(ctx, id)
}

func (s *AdminUserService) DeleteUser(ctx context.Context, id string) error {
	now := time.Now().UTC()
	return s.repo.DeleteUser(ctx, id, now)
}
