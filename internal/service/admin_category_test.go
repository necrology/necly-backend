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

type mockAdminCategoryRepository struct {
	mock.Mock
}

func (m *mockAdminCategoryRepository) ListCategories(ctx context.Context, filter CategoryFilter, page, perPage int) ([]domain.Category, int64, error) {
	args := m.Called(ctx, filter, page, perPage)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]domain.Category), args.Get(1).(int64), args.Error(2)
}

func (m *mockAdminCategoryRepository) GetCategoryByID(ctx context.Context, id string) (*domain.Category, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Category), args.Error(1)
}

func (m *mockAdminCategoryRepository) GetCategoryBySlug(ctx context.Context, slug string) (*domain.Category, error) {
	args := m.Called(ctx, slug)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Category), args.Error(1)
}

func (m *mockAdminCategoryRepository) CreateCategory(ctx context.Context, c *domain.Category) error {
	args := m.Called(ctx, c)
	return args.Error(0)
}

func (m *mockAdminCategoryRepository) UpdateCategory(ctx context.Context, c *domain.Category) error {
	args := m.Called(ctx, c)
	return args.Error(0)
}

func (m *mockAdminCategoryRepository) DeleteCategory(ctx context.Context, id string, now time.Time) error {
	args := m.Called(ctx, id, now)
	return args.Error(0)
}

func (m *mockAdminCategoryRepository) ListCategoriesWithFilter(ctx context.Context, filter CategoryFilter, page, perPage int) ([]domain.Category, int64, error) {
	args := m.Called(ctx, filter, page, perPage)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]domain.Category), args.Get(1).(int64), args.Error(2)
}

func TestAdminCategoryService_ListCategories(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminCategoryRepository)
	svc := NewAdminCategoryService(repo)

	expected := []domain.Category{
		{ID: uuid.NewString(), Name: "Category 1", Slug: "category-1", Active: true},
		{ID: uuid.NewString(), Name: "Category 2", Slug: "category-2", Active: false},
	}

	filter := CategoryFilter{Active: func() *bool { b := true; return &b }()}
	repo.On("ListCategoriesWithFilter", ctx, filter, 1, 20).Return(expected, int64(2), nil)

	categories, total, err := svc.ListCategories(ctx, CategoryFilter{Active: func() *bool { b := true; return &b }()}, 1, 20)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, categories, 2)
	repo.AssertExpectations(t)
}

func TestAdminCategoryService_GetCategory(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminCategoryRepository)
	svc := NewAdminCategoryService(repo)

	id := uuid.NewString()
	expected := &domain.Category{ID: id, Name: "Test Category", Slug: "test-category", Active: true}

	repo.On("GetCategoryByID", ctx, id).Return(expected, nil)

	category, err := svc.GetCategory(ctx, id)
	assert.NoError(t, err)
	assert.Equal(t, id, category.ID)
	repo.AssertExpectations(t)
}

func TestAdminCategoryService_GetCategoryBySlug(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminCategoryRepository)
	svc := NewAdminCategoryService(repo)

	slug := "test-category"
	expected := &domain.Category{ID: uuid.NewString(), Name: "Test Category", Slug: slug, Active: true}

	repo.On("GetCategoryBySlug", ctx, slug).Return(expected, nil)

	category, err := svc.GetCategoryBySlug(ctx, slug)
	assert.NoError(t, err)
	assert.Equal(t, slug, category.Slug)
	repo.AssertExpectations(t)
}

func TestAdminCategoryService_CreateCategory(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminCategoryRepository)
	svc := NewAdminCategoryService(repo)

	name := "New Category"
	slug := "new-category"

	repo.On("GetCategoryBySlug", ctx, slug).Return(nil, ErrNotFound)
	repo.On("CreateCategory", ctx, mock.AnythingOfType("*domain.Category")).Return(nil).Run(func(args mock.Arguments) {
		category := args.Get(1).(*domain.Category)
		assert.Equal(t, name, category.Name)
		assert.Equal(t, slug, category.Slug)
		assert.True(t, category.Active)
	})

	category, err := svc.CreateCategory(ctx, name, slug, "Description", "image.jpg")
	assert.NoError(t, err)
	assert.Equal(t, name, category.Name)
	assert.Equal(t, slug, category.Slug)
	repo.AssertExpectations(t)
}

func TestAdminCategoryService_CreateCategory_DuplicateSlug(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminCategoryRepository)
	svc := NewAdminCategoryService(repo)

	slug := "existing-category"
	existing := &domain.Category{ID: uuid.NewString(), Slug: slug}

	repo.On("GetCategoryBySlug", ctx, slug).Return(existing, nil)

	_, err := svc.CreateCategory(ctx, "Name", slug, "", "")
	assert.Error(t, err)
	assert.Equal(t, ErrConflict, err)
	repo.AssertExpectations(t)
}

func TestAdminCategoryService_UpdateCategory(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminCategoryRepository)
	svc := NewAdminCategoryService(repo)

	id := uuid.NewString()
	existing := &domain.Category{ID: id, Name: "Old", Slug: "old"}

	repo.On("GetCategoryByID", ctx, id).Return(existing, nil)
	repo.On("GetCategoryBySlug", ctx, "new-slug").Return(nil, ErrNotFound)
	repo.On("UpdateCategory", ctx, mock.AnythingOfType("*domain.Category")).Return(nil).Run(func(args mock.Arguments) {
		category := args.Get(1).(*domain.Category)
		assert.Equal(t, "New Name", category.Name)
		assert.Equal(t, "new-slug", category.Slug)
		assert.False(t, category.Active)
	})

	active := false
	category, err := svc.UpdateCategory(ctx, id, "New Name", "new-slug", "", "", &active)
	assert.NoError(t, err)
	assert.Equal(t, "New Name", category.Name)
	assert.Equal(t, "new-slug", category.Slug)
	assert.False(t, category.Active)
	repo.AssertExpectations(t)
}

func TestAdminCategoryService_DeleteCategory(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminCategoryRepository)
	svc := NewAdminCategoryService(repo)

	id := uuid.NewString()

	repo.On("DeleteCategory", ctx, id, mock.AnythingOfType("time.Time")).Return(nil)

	err := svc.DeleteCategory(ctx, id)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}
