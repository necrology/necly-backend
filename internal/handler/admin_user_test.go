package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/handler"
	"github.com/necly/services/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type fakeAdminUserRepo struct {
	mock.Mock
}

func (m *fakeAdminUserRepo) ListUsers(ctx context.Context, filter service.UserFilter, page, perPage int) ([]domain.User, int64, error) {
	args := m.Called(ctx, filter, page, perPage)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]domain.User), args.Get(1).(int64), args.Error(2)
}

func (m *fakeAdminUserRepo) GetUserByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *fakeAdminUserRepo) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *fakeAdminUserRepo) CreateUserAdmin(ctx context.Context, u *domain.User) error {
	return m.Called(ctx, u).Error(0)
}

func (m *fakeAdminUserRepo) CountActiveAdmins(ctx context.Context, excludeUserID string) (int64, error) {
	args := m.Called(ctx, excludeUserID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *fakeAdminUserRepo) ApplyUserUpdate(ctx context.Context, id string, update service.AdminUserUpdate, now time.Time) (*domain.User, error) {
	args := m.Called(ctx, id, update, now)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *fakeAdminUserRepo) DeleteUser(ctx context.Context, id string, now time.Time) error {
	return m.Called(ctx, id, now).Error(0)
}

type fakeAdminAudit struct {
	entries []service.AuditEntry
}

func (a *fakeAdminAudit) Record(_ context.Context, entry service.AuditEntry) {
	a.entries = append(a.entries, entry)
}

func newAdminUserApp(t *testing.T) (*fiber.App, *dummyAuthRepo, *fakeAdminUserRepo, *fakeAdminAudit, *service.TokenManager) {
	t.Helper()
	app := fiber.New()
	app.Use(recover.New())

	tokens, err := service.NewTokenManager("test-jwt-secret-that-has-at-least-32-bytes", "necly", 15*time.Minute, 24*time.Hour)
	assert.NoError(t, err)
	authRepo := new(dummyAuthRepo)
	authSvc := service.NewAuthService(authRepo, service.NewPasswordHasher(4), tokens, service.AuthSettings{VerificationTTL: 24 * time.Hour})
	userRepo := new(fakeAdminUserRepo)
	audit := &fakeAdminAudit{}
	adminUserSvc := service.NewAdminUserService(userRepo, audit)

	router := handler.NewRouter(
		authSvc,
		nil, nil, nil, nil, nil, nil,
		adminUserSvc,
		nil, nil, nil, nil, nil,
		tokens, nil, nil, nil, nil,
	)
	router.Register(app)
	return app, authRepo, userRepo, audit, tokens
}

func adminRequest(t *testing.T, app *fiber.App, token, method, path, body string) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := app.Test(req)
	assert.NoError(t, err)
	raw, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	var payload map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	return resp.StatusCode, payload
}

func TestAdminUpdateUser_RequiresAuthentication(t *testing.T) {
	app, _, _, _, _ := newAdminUserApp(t)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/users/"+uuid.NewString(), strings.NewReader(`{"role":"seller"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
}

func TestAdminUpdateUser_RejectsNonAdmin(t *testing.T) {
	app, authRepo, userRepo, _, tokens := newAdminUserApp(t)
	customerID := uuid.NewString()
	expectActiveSession(authRepo, customerID, "session-customer", domain.RoleCustomer)
	token, _ := tokens.IssueAccess(customerID, string(domain.RoleCustomer), "session-customer")

	status, payload := adminRequest(t, app, token, http.MethodPatch, "/api/v1/admin/users/"+uuid.NewString(), `{"role":"admin"}`)
	assert.Equal(t, fiber.StatusForbidden, status)
	assert.Equal(t, "forbidden", payload["code"])
	userRepo.AssertNotCalled(t, "ApplyUserUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminUpdateUser_RejectsInvalidRole(t *testing.T) {
	app, authRepo, userRepo, _, tokens := newAdminUserApp(t)
	adminID := uuid.NewString()
	expectActiveSession(authRepo, adminID, "session-admin", domain.RoleAdmin)
	token, _ := tokens.IssueAccess(adminID, string(domain.RoleAdmin), "session-admin")

	status, payload := adminRequest(t, app, token, http.MethodPatch, "/api/v1/admin/users/"+uuid.NewString(), `{"role":"owner"}`)
	assert.Equal(t, fiber.StatusBadRequest, status)
	assert.Equal(t, "invalid_input", payload["code"])
	userRepo.AssertNotCalled(t, "ApplyUserUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminUpdateUser_RejectsSelfDemotion(t *testing.T) {
	app, authRepo, userRepo, _, tokens := newAdminUserApp(t)
	adminID := uuid.NewString()
	expectActiveSession(authRepo, adminID, "session-admin", domain.RoleAdmin)
	token, _ := tokens.IssueAccess(adminID, string(domain.RoleAdmin), "session-admin")
	userRepo.On("GetUserByID", mock.Anything, adminID).Return(&domain.User{
		ID: adminID, Email: "admin@test.com", Role: domain.RoleAdmin, Active: true,
	}, nil)

	status, payload := adminRequest(t, app, token, http.MethodPatch, "/api/v1/admin/users/"+adminID, `{"role":"customer"}`)
	assert.Equal(t, fiber.StatusForbidden, status)
	assert.Equal(t, "forbidden", payload["code"])
	userRepo.AssertNotCalled(t, "ApplyUserUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminUpdateUser_RejectsSelfDeactivation(t *testing.T) {
	app, authRepo, userRepo, _, tokens := newAdminUserApp(t)
	adminID := uuid.NewString()
	expectActiveSession(authRepo, adminID, "session-admin", domain.RoleAdmin)
	token, _ := tokens.IssueAccess(adminID, string(domain.RoleAdmin), "session-admin")
	userRepo.On("GetUserByID", mock.Anything, adminID).Return(&domain.User{
		ID: adminID, Email: "admin@test.com", Role: domain.RoleAdmin, Active: true,
	}, nil)

	status, payload := adminRequest(t, app, token, http.MethodPatch, "/api/v1/admin/users/"+adminID, `{"active":false}`)
	assert.Equal(t, fiber.StatusForbidden, status)
	assert.Equal(t, "forbidden", payload["code"])
	userRepo.AssertNotCalled(t, "ApplyUserUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminUpdateUser_RejectsLastActiveAdmin(t *testing.T) {
	app, authRepo, userRepo, _, tokens := newAdminUserApp(t)
	adminID := uuid.NewString()
	targetID := uuid.NewString()
	expectActiveSession(authRepo, adminID, "session-admin", domain.RoleAdmin)
	token, _ := tokens.IssueAccess(adminID, string(domain.RoleAdmin), "session-admin")
	userRepo.On("GetUserByID", mock.Anything, targetID).Return(&domain.User{
		ID: targetID, Email: "target@test.com", Role: domain.RoleAdmin, Active: true,
	}, nil)
	userRepo.On("CountActiveAdmins", mock.Anything, targetID).Return(int64(0), nil)

	status, payload := adminRequest(t, app, token, http.MethodPatch, "/api/v1/admin/users/"+targetID, `{"role":"customer"}`)
	assert.Equal(t, fiber.StatusConflict, status)
	assert.Equal(t, "last_admin", payload["code"])
	userRepo.AssertNotCalled(t, "ApplyUserUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminUpdateUser_SucceedsWithoutLeakingSensitiveFields(t *testing.T) {
	app, authRepo, userRepo, audit, tokens := newAdminUserApp(t)
	adminID := uuid.NewString()
	targetID := uuid.NewString()
	expectActiveSession(authRepo, adminID, "session-admin", domain.RoleAdmin)
	token, _ := tokens.IssueAccess(adminID, string(domain.RoleAdmin), "session-admin")
	userRepo.On("GetUserByID", mock.Anything, targetID).Return(&domain.User{
		ID: targetID, Email: "target@test.com", Name: "Target", Role: domain.RoleCustomer, Active: true,
	}, nil)
	userRepo.On("ApplyUserUpdate", mock.Anything, targetID, mock.MatchedBy(func(update service.AdminUserUpdate) bool {
		return update.Role != nil && *update.Role == domain.RoleSeller
	}), mock.AnythingOfType("time.Time")).Return(&domain.User{
		ID: targetID, Email: "target@test.com", Name: "Target", Role: domain.RoleSeller, Active: true,
		PasswordHash: "must-not-leak", GoogleSubject: ptrString("google-subject"),
	}, nil)

	status, payload := adminRequest(t, app, token, http.MethodPatch, "/api/v1/admin/users/"+targetID, `{"role":"seller"}`)
	assert.Equal(t, fiber.StatusOK, status)
	assert.Equal(t, "seller", payload["role"])
	assert.NotContains(t, payload, "password_hash")
	assert.NotContains(t, payload, "google_subject")
	assert.NotContains(t, payload, "must-not-leak")
	assert.Len(t, audit.entries, 1)
	assert.Equal(t, service.AuditAdminUserRoleChanged, audit.entries[0].Action)
	assert.Equal(t, adminID, audit.entries[0].ActorUserID)
	assert.Equal(t, targetID, audit.entries[0].ResourceID)
}

func TestAdminDeleteUser_RejectsSelfDelete(t *testing.T) {
	app, authRepo, userRepo, _, tokens := newAdminUserApp(t)
	adminID := uuid.NewString()
	expectActiveSession(authRepo, adminID, "session-admin", domain.RoleAdmin)
	token, _ := tokens.IssueAccess(adminID, string(domain.RoleAdmin), "session-admin")

	status, payload := adminRequest(t, app, token, http.MethodDelete, "/api/v1/admin/users/"+adminID, "")
	assert.Equal(t, fiber.StatusForbidden, status)
	assert.Equal(t, "forbidden", payload["code"])
	userRepo.AssertNotCalled(t, "DeleteUser", mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminDeleteUser_RejectsLastActiveAdmin(t *testing.T) {
	app, authRepo, userRepo, _, tokens := newAdminUserApp(t)
	adminID := uuid.NewString()
	targetID := uuid.NewString()
	expectActiveSession(authRepo, adminID, "session-admin", domain.RoleAdmin)
	token, _ := tokens.IssueAccess(adminID, string(domain.RoleAdmin), "session-admin")
	userRepo.On("GetUserByID", mock.Anything, targetID).Return(&domain.User{
		ID: targetID, Email: "target@test.com", Role: domain.RoleAdmin, Active: true,
	}, nil)
	userRepo.On("CountActiveAdmins", mock.Anything, targetID).Return(int64(0), nil)

	status, payload := adminRequest(t, app, token, http.MethodDelete, "/api/v1/admin/users/"+targetID, "")
	assert.Equal(t, fiber.StatusConflict, status)
	assert.Equal(t, "last_admin", payload["code"])
	userRepo.AssertNotCalled(t, "DeleteUser", mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminDeleteUser_SucceedsAndAudits(t *testing.T) {
	app, authRepo, userRepo, audit, tokens := newAdminUserApp(t)
	adminID := uuid.NewString()
	targetID := uuid.NewString()
	expectActiveSession(authRepo, adminID, "session-admin", domain.RoleAdmin)
	token, _ := tokens.IssueAccess(adminID, string(domain.RoleAdmin), "session-admin")
	userRepo.On("GetUserByID", mock.Anything, targetID).Return(&domain.User{
		ID: targetID, Email: "target@test.com", Role: domain.RoleCustomer, Active: true,
	}, nil)
	userRepo.On("DeleteUser", mock.Anything, targetID, mock.AnythingOfType("time.Time")).Return(nil)

	status, payload := adminRequest(t, app, token, http.MethodDelete, "/api/v1/admin/users/"+targetID, "")
	assert.Equal(t, fiber.StatusOK, status)
	assert.Equal(t, "Pengguna berhasil dihapus", payload["message"])
	assert.Len(t, audit.entries, 1)
	assert.Equal(t, service.AuditAdminUserDeleted, audit.entries[0].Action)
}

func TestAdminListUsers_NeverReturnsPasswordHash(t *testing.T) {
	app, authRepo, userRepo, _, tokens := newAdminUserApp(t)
	adminID := uuid.NewString()
	expectActiveSession(authRepo, adminID, "session-admin", domain.RoleAdmin)
	token, _ := tokens.IssueAccess(adminID, string(domain.RoleAdmin), "session-admin")
	userRepo.On("ListUsers", mock.Anything, service.UserFilter{}, 1, 20).Return([]domain.User{
		{ID: uuid.NewString(), Email: "a@test.com", Role: domain.RoleCustomer, Active: true, PasswordHash: "must-not-leak"},
	}, int64(1), nil)

	status, payload := adminRequest(t, app, token, http.MethodGet, "/api/v1/admin/users", "")
	assert.Equal(t, fiber.StatusOK, status)
	assert.NotContains(t, payload, "password_hash")
	assert.NotContains(t, payload, "must-not-leak")
	users := payload["users"].([]any)
	assert.Len(t, users, 1)
	first := users[0].(map[string]any)
	assert.NotContains(t, first, "password_hash")
	assert.NotContains(t, first, "google_subject")
}

func TestAdminListUsers_RejectsInvalidRoleFilter(t *testing.T) {
	app, authRepo, userRepo, _, tokens := newAdminUserApp(t)
	adminID := uuid.NewString()
	expectActiveSession(authRepo, adminID, "session-admin", domain.RoleAdmin)
	token, _ := tokens.IssueAccess(adminID, string(domain.RoleAdmin), "session-admin")

	status, payload := adminRequest(t, app, token, http.MethodGet, "/api/v1/admin/users?role=owner", "")
	assert.Equal(t, fiber.StatusBadRequest, status)
	assert.Equal(t, "invalid_input", payload["code"])
	userRepo.AssertNotCalled(t, "ListUsers", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func ptrString(value string) *string { return &value }
