package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/platform/mailer"
)

type AuthRepository interface {
	CreateUser(ctx context.Context, user *domain.User, verification *domain.EmailVerificationToken) error
	CreateVerificationToken(ctx context.Context, token *domain.EmailVerificationToken, now time.Time) error
	UpdatePasswordHash(ctx context.Context, userID, hash string, now time.Time) error
	UpdatePasswordHashAndRevokeSessions(ctx context.Context, userID, hash, exceptSessionID string, now time.Time) error
	ListRefreshTokens(ctx context.Context, userID string, now time.Time) ([]domain.RefreshToken, error)
	RevokeRefreshTokenByID(ctx context.Context, userID, tokenID string, now time.Time) error
	RevokeUserRefreshTokens(ctx context.Context, userID, exceptHash string, now time.Time) error
	UserByEmail(ctx context.Context, email string) (*domain.User, error)
	UserByID(ctx context.Context, id string) (*domain.User, error)
	SaveRefreshToken(ctx context.Context, token *domain.RefreshToken) error
	RefreshTokenByHash(ctx context.Context, hash string) (*domain.RefreshToken, error)
	RefreshTokenByID(ctx context.Context, id string) (*domain.RefreshToken, error)
	RotateRefreshToken(ctx context.Context, oldHash string, replacement *domain.RefreshToken, now time.Time) error
	RevokeRefreshToken(ctx context.Context, hash string, now time.Time) error
	ConsumeVerificationToken(ctx context.Context, hash string, now time.Time) (*domain.User, error)
	UpsertGoogleUser(ctx context.Context, email, name, subject string, now time.Time) (*domain.User, error)
	CreatePasswordResetToken(ctx context.Context, token *domain.PasswordResetToken) error
	ConsumePasswordResetToken(ctx context.Context, hash, passwordHash string, now time.Time) (*domain.User, error)
}

// AuthSettings groups the constructor arguments that are pure configuration, so
// adding one does not churn every call site of NewAuthService.
type AuthSettings struct {
	VerificationTTL  time.Duration
	PasswordResetTTL time.Duration
	// FrontendBaseURL is where reset links point: the storefront origin
	// (https://necly-service.my.id), not this API's own origin.
	FrontendBaseURL string
	Mailer          MailSender
	Google          *GoogleOAuthConfig
	// Audit is optional; leaving it nil disables recording without affecting the
	// flows themselves.
	Audit AuditWriter
	// Production turns mail delivery into a hard requirement for token-bearing
	// flows. Without it those flows could fall back to a log-only sender and
	// expose activation/reset tokens in the server log.
	Production bool
}

type RegisterInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type ClientInfo struct {
	IPAddress string
	UserAgent string
}

type RegisterResult struct {
	User              domain.User `json:"user"`
	VerificationToken string      `json:"verification_token,omitempty"`
}

type Session struct {
	User         domain.User `json:"user"`
	AccessToken  string      `json:"access_token"`
	RefreshToken string      `json:"-"`
	ExpiresIn    int64       `json:"expires_in"`
	// RefreshExpiresAt lets the handler size the cookie from the configured
	// JWT_REFRESH_TTL instead of a hardcoded week.
	RefreshExpiresAt time.Time `json:"-"`
}

type GoogleOAuthConfig struct {
	ClientID          string
	ClientSecret      string
	RedirectURL       string
	FrontendReturnURL string
	AuthURL           string
	TokenURL          string
	UserInfoURL       string
	StateTTL          time.Duration
}

type AuthService struct {
	repo             AuthRepository
	hasher           PasswordHasher
	tokens           *TokenManager
	verificationTTL  time.Duration
	passwordResetTTL time.Duration
	frontendBaseURL  string
	mail             MailSender
	now              func() time.Time
	googleConfig     *GoogleOAuthConfig
	audit            AuditWriter
	production       bool
}

func NewAuthService(repo AuthRepository, hasher PasswordHasher, tokens *TokenManager, settings AuthSettings) *AuthService {
	ttl := settings.PasswordResetTTL
	if ttl <= 0 {
		ttl = 45 * time.Minute
	}
	// Without a fallback a missing JWT_VERIFICATION_TTL would mint activation
	// links that are already expired the moment they are sent.
	verificationTTL := settings.VerificationTTL
	if verificationTTL <= 0 {
		verificationTTL = 24 * time.Hour
	}
	return &AuthService{
		repo:             repo,
		hasher:           hasher,
		tokens:           tokens,
		verificationTTL:  verificationTTL,
		passwordResetTTL: ttl,
		frontendBaseURL:  strings.TrimRight(settings.FrontendBaseURL, "/"),
		mail:             settings.Mailer,
		now:              time.Now,
		googleConfig:     settings.Google,
		audit:            settings.Audit,
		production:       settings.Production,
	}
}

func (s *AuthService) Register(ctx context.Context, input RegisterInput) (*RegisterResult, error) {
	name := strings.TrimSpace(input.Name)
	email := normalizeEmail(input.Email)
	if name == "" || !validEmail(email) || len(input.Password) < 10 || len(input.Password) > 72 {
		return nil, fmt.Errorf("%w: Nama, format email valid, dan kata sandi 10-72 karakter wajib diisi", ErrInvalidInput)
	}
	// Check mail before creating the account: a production deployment without
	// working email must not leave an unverifiable account behind, and it must
	// never return the raw activation token as a fallback.
	if s.production && !s.mailEnabled() {
		return nil, ErrMailUnavailable
	}
	hash, err := s.hasher.Hash(input.Password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	rawVerification, err := randomToken(32)
	if err != nil {
		return nil, fmt.Errorf("generate verification token: %w", err)
	}
	now := s.now().UTC()
	user := &domain.User{ID: uuid.NewString(), Email: email, Name: name, PasswordHash: hash, Role: domain.RoleCustomer, Active: true, CreatedAt: now, UpdatedAt: now}
	verification := &domain.EmailVerificationToken{ID: uuid.NewString(), UserID: user.ID, TokenHash: HashToken(rawVerification), ExpiresAt: now.Add(s.verificationTTL), CreatedAt: now}
	if err := s.repo.CreateUser(ctx, user, verification); err != nil {
		return nil, err
	}
	// Login refuses an account whose EmailVerifiedAt is nil, so the activation
	// link has to reach the mailbox.
	s.sendVerificationMail(ctx, user, s.verificationURL(rawVerification))
	s.record(ctx, AuditEntry{Action: AuditRegister, ActorUserID: user.ID, Resource: "user", ResourceID: user.ID, Metadata: map[string]any{"email": user.Email}})

	result := &RegisterResult{User: *user}
	// The raw token is handed back only when nothing was actually delivered, so a
	// local or log-only setup stays workable. Returning it in production would let
	// anyone register with an address they do not own, activate it themselves, and
	// later absorb the real owner's Google sign-in (accounts are linked by email).
	if !s.mailEnabled() && !s.production {
		result.VerificationToken = rawVerification
	}
	return result, nil
}

func (s *AuthService) mailEnabled() bool {
	return s.mail != nil && s.mail.Enabled()
}

func (s *AuthService) Login(ctx context.Context, input LoginInput, client ClientInfo) (*Session, error) {
	email := normalizeEmail(input.Email)
	if !validEmail(email) || input.Password == "" {
		return nil, ErrUnauthorized
	}
	user, err := s.repo.UserByEmail(ctx, email)
	if err != nil || !s.hasher.Verify(user.PasswordHash, input.Password) {
		// The attempted address is recorded so repeated failures against one
		// mailbox are visible, which a bare "unauthorized" would hide.
		s.record(ctx, AuditEntry{Action: AuditLoginFailed, Resource: "user", IPAddress: client.IPAddress, Metadata: map[string]any{"email": email, "reason": "credentials"}})
		return nil, ErrUnauthorized
	}
	// An account an admin deactivated or deleted must not be able to sign in;
	// previously neither flag was consulted, so a banned customer kept ordering.
	if user.DeletedAt != nil || !user.Active {
		s.record(ctx, AuditEntry{Action: AuditLoginBlocked, ActorUserID: user.ID, Resource: "user", ResourceID: user.ID, IPAddress: client.IPAddress, Metadata: map[string]any{"reason": "inactive or deleted"}})
		return nil, ErrUnauthorized
	}
	if user.EmailVerifiedAt == nil {
		s.record(ctx, AuditEntry{Action: AuditLoginFailed, ActorUserID: user.ID, Resource: "user", ResourceID: user.ID, IPAddress: client.IPAddress, Metadata: map[string]any{"reason": "email unverified"}})
		return nil, ErrEmailNotVerified
	}
	// Hashes written before the bcrypt cost increase are upgraded here, on the
	// one request where the plaintext password is available.
	if s.hasher.NeedsRehash(user.PasswordHash) {
		if upgraded, hashErr := s.hasher.Hash(input.Password); hashErr == nil {
			if storeErr := s.repo.UpdatePasswordHash(ctx, user.ID, upgraded, s.now().UTC()); storeErr == nil {
				user.PasswordHash = upgraded
			}
		}
	}
	s.record(ctx, AuditEntry{Action: AuditLoginSucceeded, ActorUserID: user.ID, Resource: "user", ResourceID: user.ID, IPAddress: client.IPAddress})
	return s.issueSession(ctx, user, client)
}

func (s *AuthService) Refresh(ctx context.Context, raw string, client ClientInfo) (*Session, error) {
	if raw == "" {
		return nil, ErrUnauthorized
	}
	now := s.now().UTC()
	oldHash := HashToken(raw)
	old, err := s.repo.RefreshTokenByHash(ctx, oldHash)
	if err != nil || old.RevokedAt != nil || !old.ExpiresAt.After(now) {
		return nil, ErrUnauthorized
	}
	user, err := s.repo.UserByID(ctx, old.UserID)
	if err != nil {
		return nil, ErrUnauthorized
	}
	// Refresh has the same gap Login had: without this a deactivated or deleted
	// account would keep renewing its session forever.
	if user.DeletedAt != nil || !user.Active {
		return nil, ErrUnauthorized
	}
	newRaw, newHash, expiresAt, err := s.tokens.IssueRefresh()
	if err != nil {
		return nil, err
	}
	replacement := &domain.RefreshToken{ID: uuid.NewString(), UserID: user.ID, TokenHash: newHash, ExpiresAt: expiresAt, UserAgent: client.UserAgent, IPAddress: client.IPAddress, CreatedAt: now}
	access, err := s.tokens.IssueAccess(user.ID, string(user.Role), replacement.ID)
	if err != nil {
		return nil, err
	}
	if err := s.repo.RotateRefreshToken(ctx, oldHash, replacement, now); err != nil {
		return nil, ErrUnauthorized
	}
	return &Session{User: *user, AccessToken: access, RefreshToken: newRaw, ExpiresIn: int64(s.tokens.accessTTL.Seconds()), RefreshExpiresAt: expiresAt}, nil
}

func (s *AuthService) Logout(ctx context.Context, raw string) error {
	if raw == "" {
		return nil
	}
	hash := HashToken(raw)
	// Read before revoking so the trail can name the account that signed out.
	actor := ""
	if token, err := s.repo.RefreshTokenByHash(ctx, hash); err == nil {
		actor = token.UserID
	}
	if err := s.repo.RevokeRefreshToken(ctx, hash, s.now().UTC()); err != nil {
		return err
	}
	s.record(ctx, AuditEntry{Action: AuditLogout, ActorUserID: actor, Resource: "user", ResourceID: actor})
	return nil
}

func (s *AuthService) VerifyEmail(ctx context.Context, raw string) (*domain.User, error) {
	if raw == "" {
		return nil, ErrUnauthorized
	}
	user, err := s.repo.ConsumeVerificationToken(ctx, HashToken(raw), s.now().UTC())
	if err != nil {
		return nil, err
	}
	s.record(ctx, AuditEntry{Action: AuditEmailVerified, ActorUserID: user.ID, Resource: "user", ResourceID: user.ID})
	return user, nil
}

// ForgotPassword starts the "lupa kata sandi" flow (QA baris 60). The caller
// explicitly requires unknown addresses to be reported so the storefront can
// direct the visitor to registration.
func (s *AuthService) ForgotPassword(ctx context.Context, email string) error {
	email = normalizeEmail(email)
	if !validEmail(email) {
		return ErrInvalidInput
	}
	if s.production && !s.mailEnabled() {
		return ErrMailUnavailable
	}
	user, err := s.repo.UserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("find password reset user: %w", err)
	}

	raw, err := randomToken(32)
	if err != nil {
		return fmt.Errorf("generate password reset token: %w", err)
	}
	now := s.now().UTC()
	token := &domain.PasswordResetToken{
		ID:        uuid.NewString(),
		UserID:    user.ID,
		TokenHash: HashToken(raw),
		ExpiresAt: now.Add(s.passwordResetTTL),
		CreatedAt: now,
	}
	if err := s.repo.CreatePasswordResetToken(ctx, token); err != nil {
		return err
	}

	s.sendPasswordResetMail(ctx, user, s.passwordResetURL(raw))
	s.record(ctx, AuditEntry{Action: AuditForgotPassword, ActorUserID: user.ID, Resource: "user", ResourceID: user.ID})
	return nil
}

// ResetPassword redeems a reset token and stores the new password. The token is
// single-use and time-limited, and redeeming it revokes every existing session.
func (s *AuthService) ResetPassword(ctx context.Context, rawToken, newPassword string) (*domain.User, error) {
	if strings.TrimSpace(rawToken) == "" {
		return nil, ErrUnauthorized
	}
	if len(newPassword) < 10 || len(newPassword) > 72 {
		return nil, fmt.Errorf("%w: Kata sandi baru harus 10-72 karakter", ErrInvalidInput)
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	user, err := s.repo.ConsumePasswordResetToken(ctx, HashToken(rawToken), hash, s.now().UTC())
	if err != nil {
		return nil, err
	}
	s.record(ctx, AuditEntry{Action: AuditPasswordReset, ActorUserID: user.ID, Resource: "user", ResourceID: user.ID})
	return user, nil
}

// passwordResetURL builds the storefront link. The token travels in the query
// string because the reset page is a static client component, and it is only
// ever sent to the account owner's mailbox.
func (s *AuthService) passwordResetURL(rawToken string) string {
	base := s.frontendBaseURL
	if base == "" {
		base = "http://localhost:3000"
	}
	return fmt.Sprintf("%s/reset-password?token=%s", base, url.QueryEscape(rawToken))
}

func (s *AuthService) sendPasswordResetMail(ctx context.Context, user *domain.User, resetURL string) {
	if s.mail == nil {
		return
	}
	minutes := int(s.passwordResetTTL.Minutes())
	body := strings.Join([]string{
		"Halo " + user.Name + ",",
		"",
		"Kami menerima permintaan untuk mengatur ulang kata sandi akun Necly Services Anda.",
		"Buka tautan berikut untuk membuat kata sandi baru:",
		resetURL,
		"",
		fmt.Sprintf("Tautan ini berlaku %d menit dan hanya dapat dipakai satu kali.", minutes),
		"Jika Anda tidak meminta pengaturan ulang kata sandi, abaikan email ini. Kata sandi Anda tidak berubah.",
		"",
		"Necly Services",
	}, "\n")

	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	if err := s.mail.Send(sendCtx, mailer.Message{
		To:      user.Email,
		Subject: "Atur ulang kata sandi Necly Services",
		Body:    body,
	}); err != nil {
		// Never surfaced to the caller: the response stays generic so an
		// attacker cannot tell a delivery failure from an unknown address.
		log.Printf("forgot-password: sending reset email to %q failed: %v", user.Email, err)
	}
}

// verificationURL mirrors passwordResetURL: the activation token travels in the
// query string because the verification page is a static client component.
func (s *AuthService) verificationURL(rawToken string) string {
	base := s.frontendBaseURL
	if base == "" {
		base = "http://localhost:3000"
	}
	return fmt.Sprintf("%s/verify-email?token=%s", base, url.QueryEscape(rawToken))
}

func (s *AuthService) sendVerificationMail(ctx context.Context, user *domain.User, verifyURL string) {
	if s.mail == nil {
		return
	}
	hours := int(s.verificationTTL.Hours())
	expiry := fmt.Sprintf("Tautan ini berlaku %d jam dan hanya dapat dipakai satu kali.", hours)
	if hours < 1 {
		expiry = fmt.Sprintf("Tautan ini berlaku %d menit dan hanya dapat dipakai satu kali.", int(s.verificationTTL.Minutes()))
	}
	body := strings.Join([]string{
		"Halo " + user.Name + ",",
		"",
		"Terima kasih sudah mendaftar di Necly Services.",
		"Buka tautan berikut untuk mengaktifkan akun Anda:",
		verifyURL,
		"",
		expiry,
		"Selama akun belum diaktifkan, Anda belum bisa masuk ke Necly Services.",
		"Jika Anda tidak merasa membuat akun ini, abaikan email ini.",
		"",
		"Necly Services",
	}, "\n")

	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	if err := s.mail.Send(sendCtx, mailer.Message{
		To:      user.Email,
		Subject: "Aktifkan akun Necly Services",
		Body:    body,
	}); err != nil {
		// The account already exists; a delivery failure must not fail the
		// request. The user can ask for a fresh link from the verification screen.
		log.Printf("register: sending verification email to %q failed: %v", user.Email, err)
	}
}

// ResendVerification issues a fresh activation link and retires the previous
// unused ones, so "kirim ulang verifikasi" always yields exactly one working
// link. The storefront calls it when the registration email was lost, and right
// after a login attempt is refused because the address is still unverified.
func (s *AuthService) ResendVerification(ctx context.Context, email string) error {
	email = normalizeEmail(email)
	if !validEmail(email) {
		return ErrInvalidInput
	}
	if s.production && !s.mailEnabled() {
		return ErrMailUnavailable
	}
	user, err := s.repo.UserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("find verification user: %w", err)
	}
	if user.EmailVerifiedAt != nil {
		return ErrAlreadyVerified
	}

	raw, err := randomToken(32)
	if err != nil {
		return fmt.Errorf("generate verification token: %w", err)
	}
	now := s.now().UTC()
	token := &domain.EmailVerificationToken{
		ID:        uuid.NewString(),
		UserID:    user.ID,
		TokenHash: HashToken(raw),
		ExpiresAt: now.Add(s.verificationTTL),
		CreatedAt: now,
	}
	if err := s.repo.CreateVerificationToken(ctx, token, now); err != nil {
		return err
	}
	s.sendVerificationMail(ctx, user, s.verificationURL(raw))
	s.record(ctx, AuditEntry{Action: AuditVerificationResent, ActorUserID: user.ID, Resource: "user", ResourceID: user.ID})
	return nil
}

func (s *AuthService) LoginGoogleIdentity(ctx context.Context, email, name, subject string, client ClientInfo) (*Session, error) {
	email = normalizeEmail(email)
	name = strings.TrimSpace(name)
	if !validEmail(email) || name == "" || strings.TrimSpace(subject) == "" {
		return nil, ErrUnauthorized
	}
	user, err := s.repo.UpsertGoogleUser(ctx, email, name, subject, s.now().UTC())
	if err != nil {
		return nil, err
	}
	return s.issueSession(ctx, user, client)
}

func (s *AuthService) GetUserByID(ctx context.Context, id string) (*domain.User, error) {
	return s.repo.UserByID(ctx, id)
}

func (s *AuthService) issueSession(ctx context.Context, user *domain.User, client ClientInfo) (*Session, error) {
	raw, hash, expiresAt, err := s.tokens.IssueRefresh()
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	refresh := &domain.RefreshToken{ID: uuid.NewString(), UserID: user.ID, TokenHash: hash, ExpiresAt: expiresAt, UserAgent: client.UserAgent, IPAddress: client.IPAddress, CreatedAt: now}
	// Bind the access token to the refresh row that represents this sign-in.
	// The middleware checks that row on every request, so a revocation takes
	// effect immediately rather than when the access TTL happens to end.
	access, err := s.tokens.IssueAccess(user.ID, string(user.Role), refresh.ID)
	if err != nil {
		return nil, err
	}
	if err := s.repo.SaveRefreshToken(ctx, refresh); err != nil {
		return nil, err
	}
	return &Session{User: *user, AccessToken: access, RefreshToken: raw, ExpiresIn: int64(s.tokens.accessTTL.Seconds()), RefreshExpiresAt: expiresAt}, nil
}

// AuthorizeSession resolves the current account and sign-in state for an access
// token. Role and account flags are read from the database, never from the JWT,
// so demoting or disabling an account takes effect on the next request. A token
// whose refresh row was revoked or has expired is rejected immediately.
func (s *AuthService) AuthorizeSession(ctx context.Context, userID, sessionID string) (*domain.User, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(sessionID) == "" {
		return nil, ErrUnauthorized
	}
	user, err := s.repo.UserByID(ctx, userID)
	if err != nil || user == nil || user.DeletedAt != nil || !user.Active {
		return nil, ErrUnauthorized
	}
	token, err := s.repo.RefreshTokenByID(ctx, sessionID)
	if err != nil || token == nil || token.UserID != userID || token.RevokedAt != nil || !token.ExpiresAt.After(s.now().UTC()) {
		return nil, ErrUnauthorized
	}
	return user, nil
}

func (s *AuthService) GoogleConfig() *GoogleOAuthConfig {
	return s.googleConfig
}

func (s *AuthService) GenerateOAuthState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

func (s *AuthService) ExchangeGoogleCode(ctx context.Context, code string, cfg *GoogleOAuthConfig) (*Session, error) {
	if cfg == nil {
		return nil, ErrUnauthorized
	}

	// Exchange code for tokens
	tokenData := url.Values{}
	tokenData.Set("client_id", cfg.ClientID)
	tokenData.Set("client_secret", cfg.ClientSecret)
	tokenData.Set("code", code)
	tokenData.Set("grant_type", "authorization_code")
	tokenData.Set("redirect_uri", cfg.RedirectURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(tokenData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, ErrUnauthorized
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}

	// Get user info
	userReq, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.UserInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create userinfo request: %w", err)
	}
	userReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)

	userResp, err := client.Do(userReq)
	if err != nil {
		return nil, fmt.Errorf("userinfo request failed: %w", err)
	}
	defer userResp.Body.Close()

	if userResp.StatusCode != http.StatusOK {
		return nil, ErrUnauthorized
	}

	var userInfo struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&userInfo); err != nil {
		return nil, fmt.Errorf("decode userinfo: %w", err)
	}

	if userInfo.Sub == "" || userInfo.Email == "" {
		return nil, ErrUnauthorized
	}
	// An address Google has not confirmed must not be trusted. UpsertGoogleUser
	// links to an existing account by email, so accepting an unverified one would
	// hand that account to whoever controls the address.
	if !userInfo.EmailVerified {
		return nil, ErrUnauthorized
	}

	// Upsert user
	user, err := s.repo.UpsertGoogleUser(ctx, userInfo.Email, userInfo.Name, userInfo.Sub, s.now().UTC())
	if err != nil {
		return nil, err
	}

	// Issue session
	clientInfo := ClientInfo{IPAddress: "", UserAgent: ""}
	return s.issueSession(ctx, user, clientInfo)
}

func normalizeEmail(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func validEmail(value string) bool {
	parsed, err := mail.ParseAddress(value)
	return err == nil && parsed.Address == value && strings.Contains(value, "@")
}

func IsServiceError(err, target error) bool { return errors.Is(err, target) }
