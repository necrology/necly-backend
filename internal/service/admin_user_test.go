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

func (m *mockAdminUserRepository) UpdateUser(ctx context.Context, u *domain.User) error {
	args := m.Called(ctx, u)
	return args.Error(0)
}

func (m *mockAdminUserRepository) DeleteUser(ctx context.Context, id string, now time.Time) error {
	args := m.Called(ctx, id, now)
	return args.Error(0)
}

func (m *mockAdminUserRepository) UpdateUserRole(ctx context.Context, id, role string, now time.Time) error {
	args := m.Called(ctx, id, role, now)
	return args.Error(0)
}

func (m *mockAdminUserRepository) UpdateUserActive(ctx context.Context, id string, active bool, now time.Time) error {
	args := m.Called(ctx, id, active, now)
	return args.Error(0)
}

func TestAdminUserService_ListUsers(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo)

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

func TestAdminUserService_GetUser(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo)

	id := uuid.NewString()
	expected := &domain.User{ID: id, Email: "test@test.com", Name: "Test User", Role: domain.RoleCustomer}

	repo.On("GetUserByID", ctx, id).Return(expected, nil)

	user, err := svc.GetUser(ctx, id)
	assert.NoError(t, err)
	assert.Equal(t, id, user.ID)
	repo.AssertExpectations(t)
}

func TestAdminUserService_GetUserByEmail(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo)

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
	svc := NewAdminUserService(repo)

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
	repo.AssertExpectations(t)
}

func TestAdminUserService_CreateUser_DuplicateEmail(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo)

	email := "existing@test.com"
	existingUser := &domain.User{ID: uuid.NewString(), Email: email}

	repo.On("GetUserByEmail", ctx, email).Return(existingUser, nil)

	_, err := svc.CreateUser(ctx, email, "Name", "hash", domain.RoleCustomer)
	assert.Error(t, err)
	assert.Equal(t, ErrConflict, err)
	repo.AssertExpectations(t)
}

func TestAdminUserService_UpdateUser(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo)

	id := uuid.NewString()
	existing := &domain.User{ID: id, Email: "old@test.com", Name: "Old Name", Role: domain.RoleCustomer, Active: true}

	repo.On("GetUserByID", ctx, id).Return(existing, nil)
	repo.On("GetUserByEmail", ctx, "new@test.com").Return(nil, ErrNotFound)
	repo.On("UpdateUser", ctx, mock.AnythingOfType("*domain.User")).Return(nil).Run(func(args mock.Arguments) {
		user := args.Get(1).(*domain.User)
		assert.Equal(t, "new@test.com", user.Email)
		assert.Equal(t, "New Name", user.Name)
		assert.False(t, user.Active)
	})

	user, err := svc.UpdateUser(ctx, id, "New Name", "new@test.com", func() *bool { b := false; return &b }())
	assert.NoError(t, err)
	assert.Equal(t, "new@test.com", user.Email)
	assert.Equal(t, "New Name", user.Name)
	assert.False(t, user.Active)
	repo.AssertExpectations(t)
}

func TestAdminUserService_UpdateUserRole(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo)

	id := uuid.NewString()
	now := time.Now().UTC()

	repo.On("UpdateUserRole", ctx, id, string(domain.RoleAdmin), now).Return(nil)
	repo.On("GetUserByID", ctx, id).Return(&domain.User{ID: id, Role: domain.RoleAdmin}, nil)

	user, err := svc.UpdateUserRole(ctx, id, domain.RoleAdmin)
	assert.NoError(t, err)
	assert.Equal(t, domain.RoleAdmin, user.Role)
	repo.AssertExpectations(t)
}

func TestAdminUserService_UpdateUserRole_Invalid(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo)

	id := uuid.NewString()

	_, err := svc.UpdateUserRole(ctx, id, "invalid_role")
	assert.Error(t, err)
	assert.Equal(t, ErrInvalidInput, err)
}

func TestAdminUserService_UpdateUserActive(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo)

	id := uuid.NewString()
	now := time.Now().UTC()

	repo.On("UpdateUserActive", ctx, id, false, now).Return(nil)
	repo.On("GetUserByID", ctx, id).Return(&domain.User{ID: id, Active: false}, nil)

	user, err := svc.UpdateUserActive(ctx, id, false)
	assert.NoError(t, err)
	assert.False(t, user.Active)
	repo.AssertExpectations(t)
}

func TestAdminUserService_DeleteUser(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminUserRepository)
	svc := NewAdminUserService(repo)

	id := uuid.NewString()

	repo.On("DeleteUser", ctx, id, mock.AnythingOfType("time.Time")).Return(nil)

	err := svc.DeleteUser(ctx, id)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}
