package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockAdminUserRepository struct {
	mock.Mock
}

func (m *mockAdminUserRepository) ListUsers(ctx context.Context, filter UserFilter, page, perPage int) ([]domain.User, int64, error) {
	args := m.Called(ctx, filter, page, perPage)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]domain.User), args.Get(1).(int64), args.Error(2)
}

func (m *mockAdminUserRepository) GetUserByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *mockAdminUserRepository) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *mockAdminUserRepository) CreateUserAdmin(ctx context.Context, u *domain.User) error {
	args := m.Called(ctx, u)
	return args.Error(0)
}

func (m *mockAdminUserRepository) CountActiveAdmins(ctx context.Context, excludeUserID string) (int64, error) {
	args := m.Called(ctx, excludeUserID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockAdminUserRepository) ApplyUserUpdate(ctx context.Context, id string, update AdminUserUpdate, now time.Time) (*domain.User, error) {
	args := m.Called(ctx, id, update, now)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *mockAdminUserRepository) DeleteUser(ctx context.Context, id string, now time.Time) error {
	args := m.Called(ctx, id, now)
	return args.Error(0)
}

func TestAdminUserService_ListUsers(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	expected := []domain.User{
		{ID: uuid.NewString(), Email: "user1@test.com", Name: "User 1", Role: domain.RoleCustomer, Active: true},
		{ID: uuid.NewString(), Email: "user2@test.com", Name: "User 2", Role: domain.RoleAdmin, Active: true},
	}

	filter := UserFilter{Role: "customer"}
	repo.On("ListUsers", ctx, filter, 1, 20).Return(expected, int64(2), nil)

	users, total, err := svc.ListUsers(ctx, filter, 1, 20)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, users, 2)
	repo.AssertExpectations(t)
}

func TestAdminUserService_ListUsers_InvalidRole(t *testing.T) {
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	_, _, err := svc.ListUsers(context.Background(), UserFilter{Role: "owner"}, 1, 20)
	assert.ErrorIs(t, err, ErrInvalidInput)
	repo.AssertNotCalled(t, "ListUsers", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminUserService_GetUser(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	id := uuid.NewString()
	expected := &domain.User{ID: id, Email: "test@test.com", Name: "Test User", Role: domain.RoleCustomer}

	repo.On("GetUserByID", ctx, id).Return(expected, nil)

	user, err := svc.GetUser(ctx, id)
	assert.NoError(t, err)
	assert.Equal(t, id, user.ID)
	repo.AssertExpectations(t)
}

func TestAdminUserService_GetUser_InvalidIDIsNotFound(t *testing.T) {
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	_, err := svc.GetUser(context.Background(), "not-a-uuid")
	assert.ErrorIs(t, err, ErrNotFound)
	repo.AssertNotCalled(t, "GetUserByID", mock.Anything, mock.Anything)
}

func TestAdminUserService_GetUserByEmail(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	email := "test@test.com"
	expected := &domain.User{ID: uuid.NewString(), Email: email, Name: "Test User", Role: domain.RoleCustomer}

	repo.On("GetUserByEmail", ctx, email).Return(expected, nil)

	user, err := svc.GetUserByEmail(ctx, email)
	assert.NoError(t, err)
	assert.Equal(t, email, user.Email)
	repo.AssertExpectations(t)
}

func TestAdminUserService_CreateUser(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	email := "new@test.com"
	name := "New User"
	passwordHash := "hashed_password"
	role := domain.RoleCustomer

	repo.On("GetUserByEmail", ctx, email).Return(nil, ErrNotFound)
	repo.On("CreateUserAdmin", ctx, mock.AnythingOfType("*domain.User")).Return(nil).Run(func(args mock.Arguments) {
		user := args.Get(1).(*domain.User)
		assert.Equal(t, email, user.Email)
		assert.Equal(t, name, user.Name)
		assert.Equal(t, passwordHash, user.PasswordHash)
		assert.Equal(t, role, user.Role)
		assert.True(t, user.Active)
		assert.Nil(t, user.EmailVerifiedAt)
	})

	user, err := svc.CreateUser(ctx, email, name, passwordHash, role)
	assert.NoError(t, err)
	assert.Equal(t, email, user.Email)
	assert.Equal(t, name, user.Name)
	assert.Equal(t, role, user.Role)
	assert.True(t, user.Active)
	assert.Empty(t, user.PasswordHash, "the response must never carry the hash")
	repo.AssertExpectations(t)
}

func TestAdminUserService_CreateUser_InvalidRole(t *testing.T) {
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	_, err := svc.CreateUser(context.Background(), "new@test.com", "New", "hash", domain.UserRole("owner"))
	assert.ErrorIs(t, err, ErrInvalidInput)
	repo.AssertNotCalled(t, "CreateUserAdmin", mock.Anything, mock.Anything)
}

func TestAdminUserService_CreateUser_DuplicateEmail(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	email := "existing@test.com"
	existingUser := &domain.User{ID: uuid.NewString(), Email: email}

	repo.On("GetUserByEmail", ctx, email).Return(existingUser, nil)

	_, err := svc.CreateUser(ctx, email, "Name", "hash", domain.RoleCustomer)
	assert.Error(t, err)
	assert.Equal(t, ErrConflict, err)
	repo.AssertExpectations(t)
}

func TestAdminUserService_UpdateUser_ChangesRoleAndStatusAndAudits(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	audit := &fakeAudit{}
	svc := NewAdminUserService(repo, audit)

	actor := AdminActor{UserID: uuid.NewString(), IPAddress: "203.0.113.10"}
	targetID := uuid.NewString()
	existing := &domain.User{
		ID: targetID, Email: "target@test.com", Name: "Target",
		Role: domain.RoleCustomer, Active: true, PasswordHash: "secret-hash",
	}
	newRole := domain.RoleSeller
	inactive := false

	repo.On("GetUserByID", ctx, targetID).Return(existing, nil)
	repo.On("ApplyUserUpdate", ctx, targetID, mock.MatchedBy(func(update AdminUserUpdate) bool {
		return update.Role != nil && *update.Role == domain.RoleSeller &&
			update.Active != nil && !*update.Active
	}), mock.AnythingOfType("time.Time")).Return(&domain.User{
		ID: targetID, Email: existing.Email, Name: existing.Name,
		Role: domain.RoleSeller, Active: false, PasswordHash: "secret-hash",
	}, nil)

	user, err := svc.UpdateUser(ctx, actor, targetID, AdminUserUpdate{Role: &newRole, Active: &inactive})
	assert.NoError(t, err)
	assert.Equal(t, domain.RoleSeller, user.Role)
	assert.False(t, user.Active)
	assert.Empty(t, user.PasswordHash, "the response must never carry the hash")

	assert.True(t, audit.has(AuditAdminUserRoleChanged))
	assert.True(t, audit.has(AuditAdminUserDeactivated))
	for _, entry := range audit.entries {
		assert.Equal(t, actor.UserID, entry.ActorUserID)
		assert.Equal(t, "user", entry.Resource)
		assert.Equal(t, targetID, entry.ResourceID)
		assert.Equal(t, actor.IPAddress, entry.IPAddress)
		assert.NotContains(t, entry.Metadata, "password_hash")
	}
	repo.AssertExpectations(t)
}

func TestAdminUserService_UpdateUser_InvalidRole(t *testing.T) {
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	role := domain.UserRole("owner")
	_, err := svc.UpdateUser(context.Background(), AdminActor{UserID: uuid.NewString()}, uuid.NewString(), AdminUserUpdate{Role: &role})
	assert.ErrorIs(t, err, ErrInvalidInput)
	repo.AssertNotCalled(t, "ApplyUserUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminUserService_UpdateUser_RejectsSelfDemotion(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	actorID := uuid.NewString()
	repo.On("GetUserByID", ctx, actorID).Return(&domain.User{ID: actorID, Role: domain.RoleAdmin, Active: true}, nil)

	role := domain.RoleCustomer
	_, err := svc.UpdateUser(ctx, AdminActor{UserID: actorID}, actorID, AdminUserUpdate{Role: &role})
	assert.ErrorIs(t, err, ErrSelfRoleChange)
	repo.AssertNotCalled(t, "ApplyUserUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminUserService_UpdateUser_RejectsSelfDeactivation(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	actorID := uuid.NewString()
	repo.On("GetUserByID", ctx, actorID).Return(&domain.User{ID: actorID, Role: domain.RoleAdmin, Active: true}, nil)

	inactive := false
	_, err := svc.UpdateUser(ctx, AdminActor{UserID: actorID}, actorID, AdminUserUpdate{Active: &inactive})
	assert.ErrorIs(t, err, ErrSelfDeactivate)
	repo.AssertNotCalled(t, "ApplyUserUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminUserService_UpdateUser_AllowsSelfProfileUpdate(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	actorID := uuid.NewString()
	existing := &domain.User{ID: actorID, Email: "self@test.com", Name: "Self", Role: domain.RoleAdmin, Active: true}
	name := "Renamed"

	repo.On("GetUserByID", ctx, actorID).Return(existing, nil)
	repo.On("ApplyUserUpdate", ctx, actorID, mock.MatchedBy(func(update AdminUserUpdate) bool {
		return update.Name != nil && *update.Name == "Renamed"
	}), mock.AnythingOfType("time.Time")).Return(&domain.User{
		ID: actorID, Email: existing.Email, Name: "Renamed", Role: domain.RoleAdmin, Active: true,
	}, nil)

	user, err := svc.UpdateUser(ctx, AdminActor{UserID: actorID}, actorID, AdminUserUpdate{Name: &name})
	assert.NoError(t, err)
	assert.Equal(t, "Renamed", user.Name)
	repo.AssertExpectations(t)
}

func TestAdminUserService_UpdateUser_RejectsDemotingLastActiveAdmin(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	targetID := uuid.NewString()
	repo.On("GetUserByID", ctx, targetID).Return(&domain.User{ID: targetID, Role: domain.RoleAdmin, Active: true}, nil)
	repo.On("CountActiveAdmins", ctx, targetID).Return(int64(0), nil)

	role := domain.RoleSeller
	_, err := svc.UpdateUser(ctx, AdminActor{UserID: uuid.NewString()}, targetID, AdminUserUpdate{Role: &role})
	assert.ErrorIs(t, err, ErrLastAdmin)
	repo.AssertNotCalled(t, "ApplyUserUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminUserService_UpdateUser_RejectsDeactivatingLastActiveAdmin(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	targetID := uuid.NewString()
	repo.On("GetUserByID", ctx, targetID).Return(&domain.User{ID: targetID, Role: domain.RoleAdmin, Active: true}, nil)
	repo.On("CountActiveAdmins", ctx, targetID).Return(int64(0), nil)

	inactive := false
	_, err := svc.UpdateUser(ctx, AdminActor{UserID: uuid.NewString()}, targetID, AdminUserUpdate{Active: &inactive})
	assert.ErrorIs(t, err, ErrLastAdmin)
	repo.AssertNotCalled(t, "ApplyUserUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminUserService_UpdateUser_AllowsDemotionWhenAnotherAdminRemains(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	audit := &fakeAudit{}
	svc := NewAdminUserService(repo, audit)

	targetID := uuid.NewString()
	repo.On("GetUserByID", ctx, targetID).Return(&domain.User{ID: targetID, Email: "admin@test.com", Role: domain.RoleAdmin, Active: true}, nil)
	repo.On("CountActiveAdmins", ctx, targetID).Return(int64(1), nil)

	role := domain.RoleCustomer
	repo.On("ApplyUserUpdate", ctx, targetID, mock.MatchedBy(func(update AdminUserUpdate) bool {
		return update.Role != nil && *update.Role == domain.RoleCustomer
	}), mock.AnythingOfType("time.Time")).Return(&domain.User{
		ID: targetID, Email: "admin@test.com", Role: domain.RoleCustomer, Active: true,
	}, nil)

	user, err := svc.UpdateUser(ctx, AdminActor{UserID: uuid.NewString()}, targetID, AdminUserUpdate{Role: &role})
	assert.NoError(t, err)
	assert.Equal(t, domain.RoleCustomer, user.Role)
	assert.True(t, audit.has(AuditAdminUserRoleChanged))
	repo.AssertExpectations(t)
}

func TestAdminUserService_UpdateUser_EmailConflict(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	targetID := uuid.NewString()
	otherID := uuid.NewString()
	repo.On("GetUserByID", ctx, targetID).Return(&domain.User{ID: targetID, Email: "old@test.com", Role: domain.RoleCustomer, Active: true}, nil)
	repo.On("GetUserByEmail", ctx, "taken@test.com").Return(&domain.User{ID: otherID, Email: "taken@test.com"}, nil)

	email := "Taken@Test.com"
	_, err := svc.UpdateUser(ctx, AdminActor{UserID: uuid.NewString()}, targetID, AdminUserUpdate{Email: &email})
	assert.ErrorIs(t, err, ErrConflict)
	repo.AssertNotCalled(t, "ApplyUserUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminUserService_UpdateUser_NoChangesDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	targetID := uuid.NewString()
	repo.On("GetUserByID", ctx, targetID).Return(&domain.User{
		ID: targetID, Email: "same@test.com", Name: "Same", Role: domain.RoleCustomer, Active: true,
	}, nil)

	role := domain.RoleCustomer
	active := true
	name := "  Same  "
	user, err := svc.UpdateUser(ctx, AdminActor{UserID: uuid.NewString()}, targetID, AdminUserUpdate{Name: &name, Role: &role, Active: &active})
	assert.NoError(t, err)
	assert.Equal(t, "Same", user.Name)
	repo.AssertNotCalled(t, "ApplyUserUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminUserService_DeleteUser_RejectsSelfDelete(t *testing.T) {
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	actorID := uuid.NewString()
	err := svc.DeleteUser(context.Background(), AdminActor{UserID: actorID}, actorID)
	assert.ErrorIs(t, err, ErrSelfDelete)
	repo.AssertNotCalled(t, "DeleteUser", mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminUserService_DeleteUser_RejectsLastActiveAdmin(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	targetID := uuid.NewString()
	repo.On("GetUserByID", ctx, targetID).Return(&domain.User{ID: targetID, Role: domain.RoleAdmin, Active: true}, nil)
	repo.On("CountActiveAdmins", ctx, targetID).Return(int64(0), nil)

	err := svc.DeleteUser(ctx, AdminActor{UserID: uuid.NewString()}, targetID)
	assert.ErrorIs(t, err, ErrLastAdmin)
	repo.AssertNotCalled(t, "DeleteUser", mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminUserService_DeleteUser_SucceedsAndAudits(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	audit := &fakeAudit{}
	svc := NewAdminUserService(repo, audit)

	actor := AdminActor{UserID: uuid.NewString(), IPAddress: "198.51.100.4"}
	targetID := uuid.NewString()
	repo.On("GetUserByID", ctx, targetID).Return(&domain.User{
		ID: targetID, Email: "gone@test.com", Role: domain.RoleCustomer, Active: true,
	}, nil)
	repo.On("DeleteUser", ctx, targetID, mock.AnythingOfType("time.Time")).Return(nil)

	err := svc.DeleteUser(ctx, actor, targetID)
	assert.NoError(t, err)

	repo.AssertExpectations(t)
	assert.True(t, audit.has(AuditAdminUserDeleted))
	assert.Len(t, audit.entries, 1)
	assert.Equal(t, actor.UserID, audit.entries[0].ActorUserID)
	assert.Equal(t, targetID, audit.entries[0].ResourceID)
}

func TestAdminUserService_DeleteUser_AllowsAdminWhenAnotherRemains(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	targetID := uuid.NewString()
	repo.On("GetUserByID", ctx, targetID).Return(&domain.User{ID: targetID, Role: domain.RoleAdmin, Active: true}, nil)
	repo.On("CountActiveAdmins", ctx, targetID).Return(int64(1), nil)
	repo.On("DeleteUser", ctx, targetID, mock.AnythingOfType("time.Time")).Return(nil)

	assert.NoError(t, svc.DeleteUser(ctx, AdminActor{UserID: uuid.NewString()}, targetID))
	repo.AssertExpectations(t)
}

func TestAdminUserService_DeleteUser_DoesNotCountForNonAdmin(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo, nil)

	targetID := uuid.NewString()
	repo.On("GetUserByID", ctx, targetID).Return(&domain.User{ID: targetID, Role: domain.RoleSeller, Active: true}, nil)
	repo.On("DeleteUser", ctx, targetID, mock.AnythingOfType("time.Time")).Return(nil)

	assert.NoError(t, svc.DeleteUser(ctx, AdminActor{UserID: uuid.NewString()}, targetID))
	repo.AssertNotCalled(t, "CountActiveAdmins", mock.Anything, mock.Anything)
	repo.AssertExpectations(t)
}
