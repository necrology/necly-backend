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

// AdminActor is the authenticated administrator performing the change. It is
// kept explicit instead of being read from ambient state so every service call
// can be audited with the real actor and request origin.
type AdminActor struct {
	UserID    string
	IPAddress string
}

type AdminUserService struct {
	repo  AdminUserRepository
	audit AuditWriter
}

func NewAdminUserService(repo AdminUserRepository, audit AuditWriter) *AdminUserService {
	return &AdminUserService{repo: repo, audit: audit}
}

func (s *AdminUserService) ListUsers(ctx context.Context, filter UserFilter, page, perPage int) ([]domain.User, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	if filter.Role != "" && !validUserRole(domain.UserRole(filter.Role)) {
		return nil, 0, ErrInvalidInput
	}
	return s.repo.ListUsers(ctx, filter, page, perPage)
}

func (s *AdminUserService) GetUser(ctx context.Context, id string) (*domain.User, error) {
	if !validUserID(id) {
		return nil, ErrNotFound
	}
	return s.repo.GetUserByID(ctx, id)
}

func (s *AdminUserService) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	return s.repo.GetUserByEmail(ctx, email)
}

func (s *AdminUserService) CreateUser(ctx context.Context, email, name, passwordHash string, role domain.UserRole) (*domain.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || passwordHash == "" {
		return nil, ErrInvalidInput
	}
	if !validUserRole(role) {
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
		Email:           email,
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

// UpdateUser applies name, email, role and active changes in one guarded
// operation. Splitting role/status into separate writes used to allow a partial
// update: the profile changed, then the role write failed, and the caller saw
// an error while the account was already modified.
func (s *AdminUserService) UpdateUser(ctx context.Context, actor AdminActor, id string, update AdminUserUpdate) (*domain.User, error) {
	if !validUserID(id) {
		return nil, ErrNotFound
	}
	if update.Role != nil && !validUserRole(*update.Role) {
		return nil, ErrInvalidInput
	}

	user, err := s.repo.GetUserByID(ctx, id)
	if err != nil {
		return nil, err
	}

	nextRole := user.Role
	if update.Role != nil {
		nextRole = *update.Role
	}
	nextActive := user.Active
	if update.Active != nil {
		nextActive = *update.Active
	}

	if err := s.guardAdminMutation(ctx, actor.UserID, user, nextRole, nextActive); err != nil {
		return nil, err
	}

	update = normalizeAdminUserUpdate(update)
	if update.Name != nil && *update.Name == user.Name {
		update.Name = nil
	}
	if update.Email != nil && *update.Email == user.Email {
		update.Email = nil
	}
	if update.Role != nil && *update.Role == user.Role {
		update.Role = nil
	}
	if update.Active != nil && *update.Active == user.Active {
		update.Active = nil
	}

	if update.Name == nil && update.Email == nil && update.Role == nil && update.Active == nil {
		user.PasswordHash = ""
		return user, nil
	}

	if update.Email != nil {
		existing, err := s.repo.GetUserByEmail(ctx, *update.Email)
		if err == nil && existing != nil && existing.ID != id {
			return nil, ErrConflict
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("check existing email: %w", err)
		}
	}

	updated, err := s.repo.ApplyUserUpdate(ctx, id, update, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	s.recordUserUpdate(ctx, actor, user, updated, update)
	updated.PasswordHash = ""
	return updated, nil
}

func (s *AdminUserService) DeleteUser(ctx context.Context, actor AdminActor, id string) error {
	if !validUserID(id) {
		return ErrNotFound
	}
	if actor.UserID == id {
		return ErrSelfDelete
	}

	user, err := s.repo.GetUserByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.ensureActiveAdminRemains(ctx, user, false); err != nil {
		return err
	}

	if err := s.repo.DeleteUser(ctx, id, time.Now().UTC()); err != nil {
		return err
	}

	s.record(ctx, AuditEntry{
		ActorUserID: actor.UserID,
		Action:      AuditAdminUserDeleted,
		Resource:    "user",
		ResourceID:  user.ID,
		IPAddress:   actor.IPAddress,
		Metadata: map[string]any{
			"target_email": user.Email,
			"role":         string(user.Role),
			"active":       user.Active,
		},
	})
	return nil
}

// guardAdminMutation is the single decision point for an admin access change.
// actorID is passed explicitly instead of being read from ambient state so the
// self-demotion and self-deactivation rules are evaluated against the real
// authenticated operator, not against a caller-supplied target id.
func (s *AdminUserService) guardAdminMutation(ctx context.Context, actorID string, user *domain.User, nextRole domain.UserRole, nextActive bool) error {
	if actorID != "" && actorID == user.ID {
		if nextRole != user.Role {
			return ErrSelfRoleChange
		}
		if user.Active && !nextActive {
			return ErrSelfDeactivate
		}
	}
	return s.ensureActiveAdminRemains(ctx, user, nextRole == domain.RoleAdmin && nextActive)
}

func (s *AdminUserService) ensureActiveAdminRemains(ctx context.Context, user *domain.User, remainsActiveAdmin bool) error {
	// Only a currently active administrator can reduce the pool. A customer or
	// seller can never trigger the last-admin rule, and an already inactive
	// admin does not count toward it.
	if user.Role != domain.RoleAdmin || !user.Active {
		return nil
	}
	if remainsActiveAdmin {
		return nil
	}
	remaining, err := s.repo.CountActiveAdmins(ctx, user.ID)
	if err != nil {
		return fmt.Errorf("count active admins: %w", err)
	}
	if remaining == 0 {
		return ErrLastAdmin
	}
	return nil
}

func (s *AdminUserService) recordUserUpdate(ctx context.Context, actor AdminActor, before, after *domain.User, update AdminUserUpdate) {
	if update.Name != nil || update.Email != nil {
		metadata := map[string]any{"target_email": after.Email}
		if update.Name != nil {
			metadata["previous_name"] = before.Name
			metadata["new_name"] = after.Name
		}
		if update.Email != nil {
			metadata["previous_email"] = before.Email
			metadata["new_email"] = after.Email
		}
		s.record(ctx, AuditEntry{
			ActorUserID: actor.UserID,
			Action:      AuditAdminUserProfileUpdated,
			Resource:    "user",
			ResourceID:  after.ID,
			IPAddress:   actor.IPAddress,
			Metadata:    metadata,
		})
	}

	if update.Role != nil {
		s.record(ctx, AuditEntry{
			ActorUserID: actor.UserID,
			Action:      AuditAdminUserRoleChanged,
			Resource:    "user",
			ResourceID:  after.ID,
			IPAddress:   actor.IPAddress,
			Metadata: map[string]any{
				"target_email":  after.Email,
				"previous_role": string(before.Role),
				"new_role":      string(after.Role),
			},
		})
	}

	if update.Active != nil {
		action := AuditAdminUserActivated
		if !after.Active {
			action = AuditAdminUserDeactivated
		}
		s.record(ctx, AuditEntry{
			ActorUserID: actor.UserID,
			Action:      action,
			Resource:    "user",
			ResourceID:  after.ID,
			IPAddress:   actor.IPAddress,
			Metadata: map[string]any{
				"target_email":    after.Email,
				"previous_active": before.Active,
				"new_active":      after.Active,
			},
		})
	}
}

func (s *AdminUserService) record(ctx context.Context, entry AuditEntry) {
	if s.audit == nil {
		return
	}
	s.audit.Record(ctx, entry)
}

func normalizeAdminUserUpdate(update AdminUserUpdate) AdminUserUpdate {
	if update.Name != nil {
		name := strings.TrimSpace(*update.Name)
		if name == "" {
			update.Name = nil
		} else {
			update.Name = &name
		}
	}
	if update.Email != nil {
		email := strings.ToLower(strings.TrimSpace(*update.Email))
		if email == "" {
			update.Email = nil
		} else {
			update.Email = &email
		}
	}
	return update
}

func validUserRole(role domain.UserRole) bool {
	switch role {
	case domain.RoleCustomer, domain.RoleSeller, domain.RoleAdmin:
		return true
	default:
		return false
	}
}

func validUserID(id string) bool {
	_, err := uuid.Parse(strings.TrimSpace(id))
	return err == nil
}
