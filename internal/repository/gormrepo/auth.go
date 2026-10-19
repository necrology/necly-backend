package gormrepo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Store struct {
	db  *gorm.DB
	now func() time.Time
}

func NewStore(db *gorm.DB) *Store {
	return &Store{db: db, now: time.Now}
}

func (s *Store) nowUTC() time.Time { return s.now().UTC() }

func (s *Store) CreateUser(ctx context.Context, user *domain.User, verification *domain.EmailVerificationToken) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return service.ErrConflict
			}
			return fmt.Errorf("create user: %w", err)
		}
		if err := tx.Create(verification).Error; err != nil {
			return fmt.Errorf("create verification token: %w", err)
		}
		return nil
	})
}

func (s *Store) CreateUserAdmin(ctx context.Context, u *domain.User) error {
	return s.db.WithContext(ctx).Create(u).Error
}

// UpdatePasswordHash rewrites a single column, used by the login path to upgrade
// hashes that were written with the old, cheaper bcrypt cost.
func (s *Store) UpdatePasswordHash(ctx context.Context, userID, hash string, now time.Time) error {
	return s.db.WithContext(ctx).Model(&domain.User{}).
		Where("id = ? AND deleted_at IS NULL", userID).
		Updates(map[string]any{"password_hash": hash, "updated_at": now}).Error
}

// UpdatePasswordHashAndRevokeSessions applies a password change and ends the
// account's other sign-ins in one transaction. A failure on either side leaves
// both untouched, so a password can never change while the sessions that were
// supposed to die with it survive.
func (s *Store) UpdatePasswordHashAndRevokeSessions(ctx context.Context, userID, hash, exceptSessionID string, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&domain.User{}).
			Where("id = ? AND deleted_at IS NULL", userID).
			Updates(map[string]any{"password_hash": hash, "updated_at": now})
		if result.Error != nil {
			return fmt.Errorf("update password: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return service.ErrNotFound
		}
		query := tx.Model(&domain.RefreshToken{}).
			Where("user_id = ? AND revoked_at IS NULL AND expires_at > ?", userID, now)
		if exceptSessionID != "" {
			query = query.Where("id <> ?", exceptSessionID)
		}
		if err := query.Update("revoked_at", now).Error; err != nil {
			return fmt.Errorf("revoke sessions after password change: %w", err)
		}
		return nil
	})
}

// CreateVerificationToken issues a replacement verification link and retires the
// user's previous unused ones in the same transaction, so only the newest email
// can be used to activate the account.
func (s *Store) CreateVerificationToken(ctx context.Context, token *domain.EmailVerificationToken, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.EmailVerificationToken{}).
			Where("user_id = ? AND used_at IS NULL", token.UserID).
			Update("used_at", now).Error; err != nil {
			return fmt.Errorf("retire previous verification tokens: %w", err)
		}
		if err := tx.Create(token).Error; err != nil {
			return fmt.Errorf("create verification token: %w", err)
		}
		return nil
	})
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*domain.User, error) {
	var user domain.User
	err := s.db.WithContext(ctx).Where("email = ?", email).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &user, err
}

func (s *Store) UserByID(ctx context.Context, id string) (*domain.User, error) {
	var user domain.User
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &user, err
}

func (s *Store) SaveRefreshToken(ctx context.Context, token *domain.RefreshToken) error {
	return s.db.WithContext(ctx).Create(token).Error
}

func (s *Store) RefreshTokenByHash(ctx context.Context, hash string) (*domain.RefreshToken, error) {
	var token domain.RefreshToken
	err := s.db.WithContext(ctx).Where("token_hash = ?", hash).First(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &token, err
}

func (s *Store) RefreshTokenByID(ctx context.Context, id string) (*domain.RefreshToken, error) {
	var token domain.RefreshToken
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &token, err
}

func (s *Store) RotateRefreshToken(ctx context.Context, oldHash string, replacement *domain.RefreshToken, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&domain.RefreshToken{}).
			Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ?", oldHash, now).
			Update("revoked_at", now)
		if result.Error != nil {
			return fmt.Errorf("revoke old token: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return service.ErrUnauthorized
		}
		if err := tx.Create(replacement).Error; err != nil {
			return fmt.Errorf("save replacement token: %w", err)
		}
		return nil
	})
}

func (s *Store) RevokeRefreshToken(ctx context.Context, hash string, now time.Time) error {
	return s.db.WithContext(ctx).
		Model(&domain.RefreshToken{}).
		Where("token_hash = ?", hash).
		Update("revoked_at", now).Error
}

func (s *Store) ConsumeVerificationToken(ctx context.Context, hash string, now time.Time) (*domain.User, error) {
	var user *domain.User
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var token domain.EmailVerificationToken
		if err := tx.Set("gorm:query_option", "FOR UPDATE").
			Where("token_hash = ? AND used_at IS NULL AND expires_at > ?", hash, now).
			First(&token).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrUnauthorized
			}
			return fmt.Errorf("find token: %w", err)
		}
		token.UsedAt = &now
		if err := tx.Save(&token).Error; err != nil {
			return fmt.Errorf("mark token used: %w", err)
		}
		var u domain.User
		if err := tx.Where("id = ?", token.UserID).First(&u).Error; err != nil {
			return fmt.Errorf("find user: %w", err)
		}
		u.EmailVerifiedAt = &now
		if err := tx.Save(&u).Error; err != nil {
			return fmt.Errorf("update user: %w", err)
		}
		user = &u
		return nil
	})
	return user, err
}

func (s *Store) CreatePasswordResetToken(ctx context.Context, token *domain.PasswordResetToken) error {
	if err := s.db.WithContext(ctx).Create(token).Error; err != nil {
		return fmt.Errorf("create password reset token: %w", err)
	}
	return nil
}

// ConsumePasswordResetToken redeems a reset token and applies the new password
// in one transaction, so a token can never be spent without the password change
// landing, and the change can never land on an expired or reused token. Every
// refresh token of the user is revoked: a reset is a "someone may have had my
// password" event, so the existing sessions must die with it.
func (s *Store) ConsumePasswordResetToken(ctx context.Context, hash, passwordHash string, now time.Time) (*domain.User, error) {
	var user *domain.User
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var token domain.PasswordResetToken
		if err := tx.Set("gorm:query_option", "FOR UPDATE").
			Where("token_hash = ? AND used_at IS NULL AND expires_at > ?", hash, now).
			First(&token).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrUnauthorized
			}
			return fmt.Errorf("find reset token: %w", err)
		}
		token.UsedAt = &now
		if err := tx.Save(&token).Error; err != nil {
			return fmt.Errorf("mark reset token used: %w", err)
		}

		var u domain.User
		if err := tx.Where("id = ?", token.UserID).First(&u).Error; err != nil {
			return fmt.Errorf("find user: %w", err)
		}
		u.PasswordHash = passwordHash
		u.UpdatedAt = now
		if err := tx.Save(&u).Error; err != nil {
			return fmt.Errorf("update password: %w", err)
		}
		if err := tx.Model(&domain.RefreshToken{}).
			Where("user_id = ? AND revoked_at IS NULL", u.ID).
			Update("revoked_at", now).Error; err != nil {
			return fmt.Errorf("revoke sessions: %w", err)
		}
		user = &u
		return nil
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *Store) UpsertGoogleUser(ctx context.Context, email, name, subject string, now time.Time) (*domain.User, error) {
	var user *domain.User
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var u domain.User
		// Lock the row for the duration of the transaction so a concurrent
		// admin deactivation cannot slip between the eligibility check and the
		// write. The conditional UPDATE below is a second guard against writing
		// to an account that stopped being eligible.
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("email = ?", email).First(&u).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			u = domain.User{ID: uuid.NewString(), Email: email, Name: name, Role: domain.RoleCustomer, Active: true, GoogleSubject: &subject, EmailVerifiedAt: &now, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&u).Error; err != nil {
				return fmt.Errorf("create google user: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("find user: %w", err)
		} else {
			// Check eligibility before writing anything. A disabled or deleted
			// account must not be reactivated, relinked or granted a session just
			// because Google confirmed the email address.
			if u.DeletedAt != nil || !u.Active {
				return service.ErrUnauthorized
			}
			result := tx.Model(&domain.User{}).
				Where("id = ? AND active = true AND deleted_at IS NULL", u.ID).
				Updates(map[string]any{"google_subject": subject, "email_verified_at": now, "updated_at": now})
			if result.Error != nil {
				return fmt.Errorf("update google user: %w", result.Error)
			}
			if result.RowsAffected == 0 {
				return service.ErrUnauthorized
			}
			u.GoogleSubject = &subject
			u.EmailVerifiedAt = &now
			u.UpdatedAt = now
		}
		user = &u
		return nil
	})
	return user, err
}

func (s *Store) ListUsers(ctx context.Context, filter service.UserFilter, page, perPage int) ([]domain.User, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	query := s.db.WithContext(ctx).Model(&domain.User{}).Where("deleted_at IS NULL")
	if filter.Role != "" {
		query = query.Where("role = ?", filter.Role)
	}
	if filter.Active != nil {
		query = query.Where("active = ?", *filter.Active)
	}
	if filter.Search != "" {
		like := "%" + strings.ToLower(filter.Search) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(email) LIKE ?", like, like)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}
	var users []domain.User
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC").Offset(offset).Limit(perPage).Find(&users).Error; err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	for i := range users {
		users[i].PasswordHash = ""
	}
	return users, total, nil
}

// adminAccessLockKey serializes every admin access change. Checking "is this
// the last active admin?" and then writing are two statements; without a lock,
// two concurrent demotions could each see the other admin and both succeed.
// A transaction-scoped advisory lock is used so no schema change is needed.
const adminAccessLockKey int64 = 0x4E45434C5941444D // "NECLYADM"

func (s *Store) CountActiveAdmins(ctx context.Context, excludeUserID string) (int64, error) {
	query := s.db.WithContext(ctx).Model(&domain.User{}).
		Where("role = ? AND active = true AND deleted_at IS NULL", domain.RoleAdmin)
	if excludeUserID != "" {
		query = query.Where("id <> ?", excludeUserID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count active admins: %w", err)
	}
	return count, nil
}

// ApplyUserUpdate writes one account patch atomically and enforces the
// last-admin rule inside the same transaction as the write. The service checks
// first for a readable error, but this check is the one that holds under
// concurrency.
func (s *Store) ApplyUserUpdate(ctx context.Context, id string, update service.AdminUserUpdate, now time.Time) (*domain.User, error) {
	var user domain.User
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", adminAccessLockKey).Error; err != nil {
			return fmt.Errorf("lock admin access: %w", err)
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", id).
			First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return fmt.Errorf("find user: %w", err)
		}

		nextRole := user.Role
		if update.Role != nil {
			nextRole = *update.Role
		}
		nextActive := user.Active
		if update.Active != nil {
			nextActive = *update.Active
		}
		if user.Role == domain.RoleAdmin && user.Active && (nextRole != domain.RoleAdmin || !nextActive) {
			var others int64
			if err := tx.Model(&domain.User{}).
				Where("role = ? AND active = true AND deleted_at IS NULL AND id <> ?", domain.RoleAdmin, id).
				Count(&others).Error; err != nil {
				return fmt.Errorf("count active admins: %w", err)
			}
			if others == 0 {
				return service.ErrLastAdmin
			}
		}

		fields := map[string]any{"updated_at": now}
		if update.Name != nil {
			fields["name"] = *update.Name
			user.Name = *update.Name
		}
		if update.Email != nil {
			fields["email"] = *update.Email
			user.Email = *update.Email
		}
		if update.Role != nil {
			fields["role"] = *update.Role
			user.Role = *update.Role
		}
		if update.Active != nil {
			fields["active"] = *update.Active
			user.Active = *update.Active
		}
		if err := tx.Model(&domain.User{}).Where("id = ?", id).Updates(fields).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return service.ErrConflict
			}
			return fmt.Errorf("update user: %w", err)
		}
		user.UpdatedAt = now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// DeleteUser soft-deletes an account, refusing to remove the final active
// administrator. The same advisory lock serializes it with role/status writes.
func (s *Store) DeleteUser(ctx context.Context, id string, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", adminAccessLockKey).Error; err != nil {
			return fmt.Errorf("lock admin access: %w", err)
		}
		var user domain.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", id).
			First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return fmt.Errorf("find user: %w", err)
		}
		if user.Role == domain.RoleAdmin && user.Active {
			var others int64
			if err := tx.Model(&domain.User{}).
				Where("role = ? AND active = true AND deleted_at IS NULL AND id <> ?", domain.RoleAdmin, id).
				Count(&others).Error; err != nil {
				return fmt.Errorf("count active admins: %w", err)
			}
			if others == 0 {
				return service.ErrLastAdmin
			}
		}
		if err := tx.Model(&domain.User{}).
			Where("id = ? AND deleted_at IS NULL", id).
			Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error; err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		return nil
	})
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	return s.UserByEmail(ctx, email)
}

// GetUserByID is the administration read path. Soft-deleted accounts are
// invisible here so the admin endpoints cannot inspect or mutate them. UserByID
// intentionally stays unfiltered: AuthorizeSession must still read a deleted
// account in order to reject its session.
func (s *Store) GetUserByID(ctx context.Context, id string) (*domain.User, error) {
	var user domain.User
	err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", id).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &user, err
}

// ListRefreshTokens returns the account's live sign-ins, newest first. Revoked
// and expired rows are filtered here so callers never have to.
func (s *Store) ListRefreshTokens(ctx context.Context, userID string, now time.Time) ([]domain.RefreshToken, error) {
	var tokens []domain.RefreshToken
	err := s.db.WithContext(ctx).
		Where("user_id = ? AND revoked_at IS NULL AND expires_at > ?", userID, now).
		Order("created_at DESC").
		Find(&tokens).Error
	if err != nil {
		return nil, fmt.Errorf("list refresh tokens: %w", err)
	}
	return tokens, nil
}

// RevokeRefreshTokenByID ends one sign-in. The user id is part of the WHERE so an
// id belonging to another account can never be ended from someone else's session.
func (s *Store) RevokeRefreshTokenByID(ctx context.Context, userID, tokenID string, now time.Time) error {
	result := s.db.WithContext(ctx).Model(&domain.RefreshToken{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL", tokenID, userID).
		Update("revoked_at", now)
	if result.Error != nil {
		return fmt.Errorf("revoke refresh token: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return service.ErrNotFound
	}
	return nil
}

// RevokeUserRefreshTokens ends every live sign-in of an account. The token that
// made the request can be spared so a password change does not sign you out of
// the device you are holding.
func (s *Store) RevokeUserRefreshTokens(ctx context.Context, userID, exceptHash string, now time.Time) error {
	query := s.db.WithContext(ctx).Model(&domain.RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL AND expires_at > ?", userID, now)
	if exceptHash != "" {
		query = query.Where("token_hash <> ?", exceptHash)
	}
	if err := query.Update("revoked_at", now).Error; err != nil {
		return fmt.Errorf("revoke user refresh tokens: %w", err)
	}
	return nil
}
