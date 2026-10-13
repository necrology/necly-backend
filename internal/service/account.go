package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SessionInfo is the public view of one active sign-in. The token hash is never
// exposed; the client only needs enough to recognise the device.
type SessionInfo struct {
	ID        string    `json:"id"`
	UserAgent string    `json:"user_agent"`
	IPAddress string    `json:"ip_address"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Current   bool      `json:"current"`
}

// ChangePassword rotates the password of a signed-in account. The current
// password is required even though the caller already holds a session, so a
// stolen access token alone cannot lock the owner out. Every other session is
// ended, because a password change is usually a response to suspicion.
func (s *AuthService) ChangePassword(ctx context.Context, userID, currentPassword, newPassword, currentRefreshToken string, client ClientInfo) error {
	user, err := s.repo.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !s.hasher.Verify(user.PasswordHash, currentPassword) {
		s.record(ctx, AuditEntry{Action: AuditLoginFailed, ActorUserID: userID, Resource: "user", ResourceID: userID, IPAddress: client.IPAddress, Metadata: map[string]any{"reason": "wrong current password on change"}})
		return fmt.Errorf("%w: Kata sandi saat ini tidak sesuai", ErrInvalidInput)
	}
	if len(newPassword) < 10 || len(newPassword) > 72 {
		return fmt.Errorf("%w: Kata sandi baru harus 10-72 karakter", ErrInvalidInput)
	}
	if newPassword == currentPassword {
		return fmt.Errorf("%w: Kata sandi baru harus berbeda dari yang sekarang", ErrInvalidInput)
	}

	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := s.repo.UpdatePasswordHash(ctx, userID, hash, s.now().UTC()); err != nil {
		return err
	}
	if currentRefreshToken != "" {
		if err := s.repo.RevokeUserRefreshTokens(ctx, userID, HashToken(currentRefreshToken), s.now().UTC()); err != nil {
			return err
		}
	}
	s.record(ctx, AuditEntry{Action: AuditPasswordChanged, ActorUserID: userID, Resource: "user", ResourceID: userID, IPAddress: client.IPAddress})
	return nil
}

// ListSessions returns the account's active sign-ins, newest first, flagging the
// one that made the request.
func (s *AuthService) ListSessions(ctx context.Context, userID, currentRefreshToken string) ([]SessionInfo, error) {
	tokens, err := s.repo.ListRefreshTokens(ctx, userID, s.now().UTC())
	if err != nil {
		return nil, err
	}
	currentHash := ""
	if currentRefreshToken != "" {
		currentHash = HashToken(currentRefreshToken)
	}
	sessions := make([]SessionInfo, 0, len(tokens))
	for _, token := range tokens {
		sessions = append(sessions, SessionInfo{
			ID:        token.ID,
			UserAgent: token.UserAgent,
			IPAddress: token.IPAddress,
			CreatedAt: token.CreatedAt,
			ExpiresAt: token.ExpiresAt,
			Current:   currentHash != "" && token.TokenHash == currentHash,
		})
	}
	return sessions, nil
}

// RevokeSession ends one sign-in by its id. The lookup is scoped to the owner so
// an id belonging to another account cannot be ended from here.
func (s *AuthService) RevokeSession(ctx context.Context, userID, sessionID, currentRefreshToken string, client ClientInfo) error {
	if strings.TrimSpace(sessionID) == "" {
		return ErrInvalidInput
	}
	if currentRefreshToken != "" {
		tokens, err := s.repo.ListRefreshTokens(ctx, userID, s.now().UTC())
		if err != nil {
			return err
		}
		for _, token := range tokens {
			// Ending the session you are standing on would sign you out of the
			// page you clicked from; the storefront offers "keluar" for that.
			if token.ID == sessionID && token.TokenHash == HashToken(currentRefreshToken) {
				return fmt.Errorf("%w: Sesi yang sedang dipakai tidak dapat diakhiri dari daftar ini", ErrInvalidInput)
			}
		}
	}
	if err := s.repo.RevokeRefreshTokenByID(ctx, userID, sessionID, s.now().UTC()); err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	s.record(ctx, AuditEntry{Action: AuditSessionsRevoked, ActorUserID: userID, Resource: "session", ResourceID: sessionID, IPAddress: client.IPAddress})
	return nil
}

// RevokeOtherSessions ends every sign-in except the one making the request, and
// reports how many were ended.
func (s *AuthService) RevokeOtherSessions(ctx context.Context, userID, currentRefreshToken string, client ClientInfo) (int, error) {
	sessions, err := s.ListSessions(ctx, userID, currentRefreshToken)
	if err != nil {
		return 0, err
	}
	except := ""
	if currentRefreshToken != "" {
		except = HashToken(currentRefreshToken)
	}
	ended := 0
	for _, session := range sessions {
		if session.Current {
			continue
		}
		ended++
	}
	if ended == 0 {
		return 0, nil
	}
	if err := s.repo.RevokeUserRefreshTokens(ctx, userID, except, s.now().UTC()); err != nil {
		return 0, err
	}
	s.record(ctx, AuditEntry{Action: AuditSessionsRevoked, ActorUserID: userID, Resource: "user", ResourceID: userID, IPAddress: client.IPAddress, Metadata: map[string]any{"ended": ended}})
	return ended, nil
}
