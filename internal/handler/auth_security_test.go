package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/handler"
	"github.com/necly/services/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func newAuthRouter(t *testing.T, repo *dummyAuthRepo, google *service.GoogleOAuthConfig) (*fiber.App, *service.TokenManager, *handler.Router) {
	t.Helper()
	app := fiber.New()
	app.Use(recover.New())
	tokens, err := service.NewTokenManager("test-jwt-secret-that-has-at-least-32-bytes", "necly", 15*time.Minute, 24*time.Hour)
	assert.NoError(t, err)
	authSvc := service.NewAuthService(repo, service.NewPasswordHasher(4), tokens, service.AuthSettings{VerificationTTL: 24 * time.Hour, Google: google})
	router := handler.NewRouter(authSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, tokens, nil, nil, nil, nil)
	router.Register(app)
	return app, tokens, router
}

func TestAuthMiddlewareRejectsDisabledDeletedDemotedAndRevokedPrincipals(t *testing.T) {
	t.Run("disabled account", func(t *testing.T) {
		app, tokens, repo := setupTestApp(t)
		repo.On("UserByID", mock.Anything, "user-1").Return(&domain.User{ID: "user-1", Role: domain.RoleCustomer, Active: false}, nil)
		repo.On("RefreshTokenByID", mock.Anything, "session-1").Return(&domain.RefreshToken{ID: "session-1", UserID: "user-1", ExpiresAt: time.Now().Add(time.Hour)}, nil)
		token, _ := tokens.IssueAccess("user-1", string(domain.RoleCustomer), "session-1")

		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/me", nil).WithContext(context.Background()))
		assert.NoError(t, err)
		assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err = app.Test(req)
		assert.NoError(t, err)
		assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("deleted account", func(t *testing.T) {
		app, tokens, repo := setupTestApp(t)
		deleted := time.Now()
		repo.On("UserByID", mock.Anything, "user-1").Return(&domain.User{ID: "user-1", Role: domain.RoleCustomer, Active: true, DeletedAt: &deleted}, nil)
		repo.On("RefreshTokenByID", mock.Anything, "session-1").Return(&domain.RefreshToken{ID: "session-1", UserID: "user-1", ExpiresAt: time.Now().Add(time.Hour)}, nil)
		token, _ := tokens.IssueAccess("user-1", string(domain.RoleCustomer), "session-1")

		req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := app.Test(req)
		assert.NoError(t, err)
		assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("stale admin claim on a demoted account", func(t *testing.T) {
		app, tokens, repo := setupTestApp(t)
		expectActiveSession(repo, "user-1", "session-1", domain.RoleCustomer)
		token, _ := tokens.IssueAccess("user-1", string(domain.RoleAdmin), "session-1")

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/dashboard", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := app.Test(req)
		assert.NoError(t, err)
		// The token says admin, the database says customer: the database wins.
		assert.Equal(t, fiber.StatusForbidden, resp.StatusCode)
	})

	t.Run("revoked session", func(t *testing.T) {
		app, tokens, repo := setupTestApp(t)
		repo.On("UserByID", mock.Anything, "user-1").Return(&domain.User{ID: "user-1", Role: domain.RoleCustomer, Active: true}, nil)
		revoked := time.Now()
		repo.On("RefreshTokenByID", mock.Anything, "session-1").Return(&domain.RefreshToken{ID: "session-1", UserID: "user-1", ExpiresAt: time.Now().Add(time.Hour), RevokedAt: &revoked}, nil)
		token, _ := tokens.IssueAccess("user-1", string(domain.RoleCustomer), "session-1")

		req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := app.Test(req)
		assert.NoError(t, err)
		assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
	})
}

func TestAuthCookieAttributesOnSetAndDelete(t *testing.T) {
	app, _, repo := setupTestApp(t)
	policy := handler.AuthCookiePolicy{Secure: true, HTTPOnly: true, SameSite: "Strict", Domain: "necly.example", Path: "/api/v1/auth"}
	// setupTestApp already registered the routes, so reconfigure the same
	// router through a fresh app to keep the test independent.
	app, _, router := newAuthRouter(t, repo, nil)
	router.SetAuthCookiePolicy(policy)

	hasher := service.NewPasswordHasher(4)
	hash, err := hasher.Hash("Str0ng-password!")
	assert.NoError(t, err)
	verified := time.Now()
	repo.On("UserByEmail", mock.Anything, "alice@example.com").Return(&domain.User{
		ID: "user-1", Email: "alice@example.com", Name: "Alice", PasswordHash: hash,
		Role: domain.RoleCustomer, Active: true, EmailVerifiedAt: &verified,
	}, nil)
	repo.On("SaveRefreshToken", mock.Anything, mock.Anything).Return(nil).Once()

	body := `{"email":"alice@example.com","password":"Str0ng-password!"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)

	refreshByPath := map[string]*http.Cookie{}
	var nonceSet *http.Cookie
	for _, cookie := range resp.Cookies() {
		switch cookie.Name {
		case "refresh_token":
			refreshByPath[cookie.Path] = cookie
		case "oauth_nonce":
			nonceSet = cookie
		}
	}
	refreshSet := refreshByPath["/api/v1/auth"]
	if refreshSet == nil {
		t.Fatalf("login did not set refresh_token on the configured path; cookies = %#v", resp.Cookies())
	}
	assert.True(t, refreshSet.Secure, "refresh cookie must be Secure")
	assert.True(t, refreshSet.HttpOnly, "refresh cookie must be HttpOnly")
	assert.Equal(t, http.SameSiteStrictMode, refreshSet.SameSite)
	assert.Equal(t, "necly.example", refreshSet.Domain)
	assert.NotEmpty(t, refreshSet.Value)
	assert.Nil(t, nonceSet, "login must not set an OAuth nonce")
	// Users upgrading from the release that wrote Path=/ must have that cookie
	// retired as well, otherwise logout would leave it behind.
	legacyClear := refreshByPath["/"]
	if legacyClear == nil {
		t.Fatalf("login did not clear the legacy Path=/ refresh cookie; cookies = %#v", resp.Cookies())
	}
	assert.Empty(t, legacyClear.Value)

	// Logout must delete both the configured cookie and the legacy cookie.
	repo.On("RefreshTokenByHash", mock.Anything, mock.Anything).Return(&domain.RefreshToken{ID: "session-1", UserID: "user-1"}, nil)
	repo.On("RevokeRefreshToken", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "raw-token"})
	resp, err = app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)

	clearedByPath := map[string]*http.Cookie{}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "refresh_token" {
			clearedByPath[cookie.Path] = cookie
		}
	}
	for _, path := range []string{"/api/v1/auth", "/"} {
		cleared := clearedByPath[path]
		if cleared == nil {
			t.Fatalf("logout did not clear refresh_token at path %q; cookies = %#v", path, resp.Cookies())
		}
		assert.True(t, cleared.Secure)
		assert.True(t, cleared.HttpOnly)
		assert.Equal(t, http.SameSiteStrictMode, cleared.SameSite)
		assert.Equal(t, "necly.example", cleared.Domain)
		assert.Empty(t, cleared.Value)
		assert.True(t, cleared.MaxAge <= 0)
		assert.False(t, cleared.Expires.IsZero())
		assert.True(t, cleared.Expires.Before(time.Now()))
	}
}

type fakeOAuthStateStore struct {
	mu      sync.Mutex
	values  map[string]string
	saveErr error
	readErr error
}

func newFakeOAuthStateStore() *fakeOAuthStateStore {
	return &fakeOAuthStateStore{values: map[string]string{}}
}

func (s *fakeOAuthStateStore) Save(_ context.Context, state, nonce string, _ time.Duration) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[state] = nonce
	return nil
}

func (s *fakeOAuthStateStore) Consume(_ context.Context, state string) (string, error) {
	if s.readErr != nil {
		return "", s.readErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	nonce, ok := s.values[state]
	if !ok {
		return "", handler.ErrOAuthStateNotFound
	}
	delete(s.values, state)
	return nonce, nil
}

func googleOAuthConfig(tokenURL, userInfoURL string) *service.GoogleOAuthConfig {
	return &service.GoogleOAuthConfig{
		ClientID: "client-id", ClientSecret: "client-secret",
		RedirectURL: "https://api.example/api/v1/auth/google/callback", FrontendReturnURL: "https://necly.example/account",
		AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: tokenURL, UserInfoURL: userInfoURL,
		StateTTL: 10 * time.Minute,
	}
}

func startGoogleStub(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			calls++
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "google-access-token", "token_type": "Bearer", "expires_in": 3600})
		case "/userinfo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub": "google-sub-1", "email": "alice@example.com", "name": "Alice", "email_verified": true,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func startOAuth(t *testing.T, app *fiber.App) (state, nonce string) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/auth/google", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusTemporaryRedirect {
		t.Fatalf("google login status = %d, want 307", resp.StatusCode)
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state = location.Query().Get("state")
	if state == "" {
		t.Fatal("redirect did not carry a state")
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "oauth_nonce" {
			nonce = cookie.Value
		}
	}
	if nonce == "" {
		t.Fatal("google login did not set a browser nonce cookie")
	}
	return state, nonce
}

func TestGoogleOAuthNonceCookieIsLaxEvenWithStrictPolicy(t *testing.T) {
	server, _ := startGoogleStub(t)
	app, _, router := newAuthRouter(t, new(dummyAuthRepo), googleOAuthConfig(server.URL+"/token", server.URL+"/userinfo"))
	router.SetOAuthStateStore(newFakeOAuthStateStore())
	router.SetAuthCookiePolicy(handler.AuthCookiePolicy{Secure: true, HTTPOnly: true, SameSite: "Strict", Path: "/api/v1/auth"})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/auth/google", nil))
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusTemporaryRedirect, resp.StatusCode)
	var nonce *http.Cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "oauth_nonce" {
			nonce = cookie
		}
	}
	if nonce == nil {
		t.Fatal("google login did not set oauth_nonce")
	}
	// A Strict nonce would be withheld on Google's cross-site callback.
	assert.Equal(t, http.SameSiteLaxMode, nonce.SameSite)
	assert.Equal(t, "/api/v1/auth/google", nonce.Path)
	assert.True(t, nonce.HttpOnly)
}

func TestChangePasswordWithoutRefreshCookieKeepsTheValidatedSession(t *testing.T) {
	app, tokens, repo := setupTestApp(t)
	hasher := service.NewPasswordHasher(4)
	hash, err := hasher.Hash("Str0ng-password!")
	assert.NoError(t, err)
	repo.On("UserByID", mock.Anything, "user-1").Return(&domain.User{
		ID: "user-1", Email: "alice@example.com", Name: "Alice", PasswordHash: hash,
		Role: domain.RoleCustomer, Active: true,
	}, nil)
	repo.On("RefreshTokenByID", mock.Anything, "session-1").Return(&domain.RefreshToken{
		ID: "session-1", UserID: "user-1", ExpiresAt: time.Now().Add(time.Hour),
	}, nil)
	repo.On("UpdatePasswordHashAndRevokeSessions", mock.Anything, "user-1", mock.Anything, "session-1", mock.Anything).Return(nil).Once()

	token, err := tokens.IssueAccess("user-1", string(domain.RoleCustomer), "session-1")
	assert.NoError(t, err)
	body := `{"current_password":"Str0ng-password!","new_password":"N3w-strong-password!"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	// No refresh_token cookie on purpose: the validated access session must be
	// spared while every other session is revoked.
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	repo.AssertCalled(t, "UpdatePasswordHashAndRevokeSessions", mock.Anything, "user-1", mock.Anything, "session-1", mock.Anything)
}

func TestGoogleOAuthStateIsBoundToTheBrowserNonceAndSingleUse(t *testing.T) {
	store := newFakeOAuthStateStore()
	server, _ := startGoogleStub(t)
	app, _, router := newAuthRouter(t, new(dummyAuthRepo), googleOAuthConfig(server.URL+"/token", server.URL+"/userinfo"))
	router.SetOAuthStateStore(store)

	stateA, nonceA := startOAuth(t, app)
	stateB, nonceB := startOAuth(t, app)
	if stateA == stateB || nonceA == nonceB {
		t.Fatal("two browsers received the same state or nonce")
	}

	// Browser B cannot redeem browser A's state just because it shares an IP.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/callback?code=code-a&state="+url.QueryEscape(stateA), nil)
	req.AddCookie(&http.Cookie{Name: "oauth_nonce", Value: nonceB})
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)

	// The mismatched attempt consumed A's state; replaying it even with the
	// right cookie must fail.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/callback?code=code-a&state="+url.QueryEscape(stateA), nil)
	req.AddCookie(&http.Cookie{Name: "oauth_nonce", Value: nonceA})
	resp, err = app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)

	// A missing cookie is rejected too.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/callback?code=code-b&state="+url.QueryEscape(stateB), nil)
	resp, err = app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
}

func TestGoogleOAuthFailsClosedWhenStateStorageIsUnavailable(t *testing.T) {
	server, _ := startGoogleStub(t)

	t.Run("store unavailable on start", func(t *testing.T) {
		store := newFakeOAuthStateStore()
		store.saveErr = errors.New("redis down")
		app, _, router := newAuthRouter(t, new(dummyAuthRepo), googleOAuthConfig(server.URL+"/token", server.URL+"/userinfo"))
		router.SetOAuthStateStore(store)

		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/auth/google", nil))
		assert.NoError(t, err)
		assert.Equal(t, fiber.StatusServiceUnavailable, resp.StatusCode)
		assert.Empty(t, resp.Header.Get("Location"), "a failed state write must not redirect to Google")
		for _, cookie := range resp.Cookies() {
			assert.NotEqual(t, "oauth_nonce", cookie.Name)
		}
	})

	t.Run("store unavailable on callback", func(t *testing.T) {
		store := newFakeOAuthStateStore()
		app, _, router := newAuthRouter(t, new(dummyAuthRepo), googleOAuthConfig(server.URL+"/token", server.URL+"/userinfo"))
		router.SetOAuthStateStore(store)
		state, nonce := startOAuth(t, app)
		store.readErr = errors.New("redis down")

		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/callback?code=code-a&state="+url.QueryEscape(state), nil)
		req.AddCookie(&http.Cookie{Name: "oauth_nonce", Value: nonce})
		resp, err := app.Test(req)
		assert.NoError(t, err)
		assert.Equal(t, fiber.StatusServiceUnavailable, resp.StatusCode)
	})

	t.Run("no store configured", func(t *testing.T) {
		app, _, _ := newAuthRouter(t, new(dummyAuthRepo), googleOAuthConfig(server.URL+"/token", server.URL+"/userinfo"))
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/auth/google", nil))
		assert.NoError(t, err)
		assert.Equal(t, fiber.StatusServiceUnavailable, resp.StatusCode)
	})
}

func TestGoogleOAuthSetsTheSessionCookieOnlyForAnEligibleAccount(t *testing.T) {
	server, calls := startGoogleStub(t)

	t.Run("blocked account", func(t *testing.T) {
		*calls = 0
		repo := new(dummyAuthRepo)
		app, _, router := newAuthRouter(t, repo, googleOAuthConfig(server.URL+"/token", server.URL+"/userinfo"))
		router.SetOAuthStateStore(newFakeOAuthStateStore())
		repo.On("UpsertGoogleUser", mock.Anything, "alice@example.com", "Alice", "google-sub-1", mock.Anything).Return(nil, service.ErrUnauthorized)

		state, nonce := startOAuth(t, app)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/callback?code=code-a&state="+url.QueryEscape(state), nil)
		req.AddCookie(&http.Cookie{Name: "oauth_nonce", Value: nonce})
		resp, err := app.Test(req)
		assert.NoError(t, err)
		assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
		for _, cookie := range resp.Cookies() {
			assert.NotEqual(t, "refresh_token", cookie.Name, "a blocked account must not receive a session cookie")
		}
	})

	t.Run("eligible account", func(t *testing.T) {
		*calls = 0
		repo := new(dummyAuthRepo)
		app, _, router := newAuthRouter(t, repo, googleOAuthConfig(server.URL+"/token", server.URL+"/userinfo"))
		router.SetOAuthStateStore(newFakeOAuthStateStore())
		repo.On("UpsertGoogleUser", mock.Anything, "alice@example.com", "Alice", "google-sub-1", mock.Anything).Return(&domain.User{
			ID: "user-1", Email: "alice@example.com", Name: "Alice", Role: domain.RoleCustomer, Active: true,
		}, nil).Once()
		repo.On("SaveRefreshToken", mock.Anything, mock.Anything).Return(nil).Once()

		state, nonce := startOAuth(t, app)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/callback?code=code-a&state="+url.QueryEscape(state), nil)
		req.AddCookie(&http.Cookie{Name: "oauth_nonce", Value: nonce})
		resp, err := app.Test(req)
		assert.NoError(t, err)
		assert.Equal(t, fiber.StatusTemporaryRedirect, resp.StatusCode)
		assert.Equal(t, "https://necly.example/account", resp.Header.Get("Location"))
		assert.NotContains(t, resp.Header.Get("Location"), "token", "the redirect must not carry tokens")

		var refreshSet *http.Cookie
		for _, cookie := range resp.Cookies() {
			if cookie.Name == "refresh_token" && cookie.Path == "/api/v1/auth" {
				refreshSet = cookie
			}
		}
		if refreshSet == nil {
			t.Fatal("eligible Google login did not set the refresh cookie")
		}
		assert.True(t, refreshSet.HttpOnly)
		assert.Equal(t, "/api/v1/auth", refreshSet.Path)
		assert.NotEmpty(t, refreshSet.Value)
		assert.Equal(t, 1, *calls)

		// The state was consumed before the exchange and cannot be replayed.
		req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/callback?code=code-a&state="+url.QueryEscape(state), nil)
		req.AddCookie(&http.Cookie{Name: "oauth_nonce", Value: nonce})
		resp, err = app.Test(req)
		assert.NoError(t, err)
		assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
		assert.Equal(t, 1, *calls, "a replayed state must not reach the provider again")
	})
}

// Ensure the body-capturing helper does not hide an accidental token leak in
// the redirect response.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	return string(body)
}
