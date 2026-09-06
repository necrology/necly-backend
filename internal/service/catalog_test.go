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

type mockCatalogRepository struct {
	mock.Mock
}

func (m *mockCatalogRepository) ListCategories(ctx context.Context, activeOnly bool, page, perPage int) ([]domain.Category, int64, error) {
	args := m.Called(ctx, activeOnly, page, perPage)
	return args.Get(0).([]domain.Category), args.Get(1).(int64), args.Error(2)
}

func (m *mockCatalogRepository) GetCategoryByID(ctx context.Context, id string) (*domain.Category, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(*domain.Category), args.Error(1)
}

func (m *mockCatalogRepository) GetCategoryBySlug(ctx context.Context, slug string) (*domain.Category, error) {
	args := m.Called(ctx, slug)
	return args.Get(0).(*domain.Category), args.Error(1)
}

func (m *mockCatalogRepository) CreateCategory(ctx context.Context, c *domain.Category) error {
	args := m.Called(ctx, c)
	return args.Error(0)
}

func (m *mockCatalogRepository) UpdateCategory(ctx context.Context, c *domain.Category) error {
	args := m.Called(ctx, c)
	return args.Error(0)
}

func (m *mockCatalogRepository) DeleteCategory(ctx context.Context, id string, now time.Time) error {
	args := m.Called(ctx, id, now)
	return args.Error(0)
}

func (m *mockCatalogRepository) ListCategoriesWithFilter(ctx context.Context, filter CategoryFilter, page, perPage int) ([]domain.Category, int64, error) {
	args := m.Called(ctx, filter, page, perPage)
	return args.Get(0).([]domain.Category), args.Get(1).(int64), args.Error(2)
}

func (m *mockCatalogRepository) ListProducts(ctx context.Context, filter ProductFilter, page, perPage int) ([]domain.Product, int64, error) {
	args := m.Called(ctx, filter, page, perPage)
	return args.Get(0).([]domain.Product), args.Get(1).(int64), args.Error(2)
}

func (m *mockCatalogRepository) GetProductByID(ctx context.Context, id string) (*domain.Product, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(*domain.Product), args.Error(1)
}

func (m *mockCatalogRepository) GetProductBySlug(ctx context.Context, slug string) (*domain.Product, error) {
	args := m.Called(ctx, slug)
	return args.Get(0).(*domain.Product), args.Error(1)
}

func (m *mockCatalogRepository) CreateProduct(ctx context.Context, p *domain.Product) error {
	args := m.Called(ctx, p)
	return args.Error(0)
}

func (m *mockCatalogRepository) UpdateProduct(ctx context.Context, p *domain.Product) error {
	args := m.Called(ctx, p)
	return args.Error(0)
}

func (m *mockCatalogRepository) DeleteProduct(ctx context.Context, id string, now time.Time) error {
	args := m.Called(ctx, id, now)
	return args.Error(0)
}

func (m *mockCatalogRepository) ListProductPrices(ctx context.Context, productID string, activeOnly bool) ([]domain.ProductPrice, error) {
	args := m.Called(ctx, productID, activeOnly)
	return args.Get(0).([]domain.ProductPrice), args.Error(1)
}

func (m *mockCatalogRepository) GetProductPriceByID(ctx context.Context, id string) (*domain.ProductPrice, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(*domain.ProductPrice), args.Error(1)
}

func (m *mockCatalogRepository) CreateProductPrice(ctx context.Context, p *domain.ProductPrice) error {
	args := m.Called(ctx, p)
	return args.Error(0)
}

func (m *mockCatalogRepository) UpdateProductPrice(ctx context.Context, p *domain.ProductPrice) error {
	args := m.Called(ctx, p)
	return args.Error(0)
}

func (m *mockCatalogRepository) DeleteProductPrice(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *mockCatalogRepository) ListProductsWithStock(ctx context.Context, filter ProductFilter, page, perPage int) ([]domain.ProductWithStock, int64, error) {
	args := m.Called(ctx, filter, page, perPage)
	return args.Get(0).([]domain.ProductWithStock), args.Get(1).(int64), args.Error(2)
}

func (m *mockCatalogRepository) GetProductWithStock(ctx context.Context, id string) (*domain.ProductWithStock, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(*domain.ProductWithStock), args.Error(1)
}

func (m *mockCatalogRepository) GetProductBySlugWithStock(ctx context.Context, slug string) (*domain.ProductWithStock, error) {
	args := m.Called(ctx, slug)
	return args.Get(0).(*domain.ProductWithStock), args.Error(1)
}

func TestCatalogService_ListCategories(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCatalogRepository)
	svc := NewCatalogService(repo)

	expected := []domain.Category{
		{ID: uuid.NewString(), Name: "Category 1", Slug: "category-1", Active: true, CreatedAt: time.Now().UTC()},
		{ID: uuid.NewString(), Name: "Category 2", Slug: "category-2", Active: true, CreatedAt: time.Now().UTC()},
	}

	repo.On("ListCategories", ctx, true, 1, 20).Return(expected, int64(2), nil)

	categories, total, err := svc.ListCategories(ctx, true, 1, 20)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, categories, 2)
	repo.AssertExpectations(t)
}

func TestCatalogService_GetCategory(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCatalogRepository)
	svc := NewCatalogService(repo)

	id := uuid.NewString()
	expected := &domain.Category{ID: id, Name: "Test Category", Slug: "test-category", Active: true}

	repo.On("GetCategoryByID", ctx, id).Return(expected, nil)

	cat, err := svc.GetCategory(ctx, id)
	assert.NoError(t, err)
	assert.Equal(t, id, cat.ID)
	repo.AssertExpectations(t)
}

func TestCatalogService_GetCategoryBySlug(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCatalogRepository)
	svc := NewCatalogService(repo)

	slug := "test-category"
	expected := &domain.Category{ID: uuid.NewString(), Name: "Test Category", Slug: slug, Active: true}

	repo.On("GetCategoryBySlug", ctx, slug).Return(expected, nil)

	cat, err := svc.GetCategoryBySlug(ctx, slug)
	assert.NoError(t, err)
	assert.Equal(t, slug, cat.Slug)
	repo.AssertExpectations(t)
}

func TestCatalogService_ListProducts(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCatalogRepository)
	svc := NewCatalogService(repo)

	expected := []domain.Product{
		{ID: uuid.NewString(), Name: "Product 1", Slug: "product-1", Active: true},
		{ID: uuid.NewString(), Name: "Product 2", Slug: "product-2", Active: true},
	}

	repo.On("ListProducts", ctx, mock.AnythingOfType("service.ProductFilter"), 1, 20).Return(expected, int64(2), nil)

	products, total, err := svc.ListProducts(ctx, "", "", true, 1, 20)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, products, 2)
	repo.AssertExpectations(t)
}

func TestCatalogService_GetProduct(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCatalogRepository)
	svc := NewCatalogService(repo)

	id := uuid.NewString()
	expected := &domain.Product{ID: id, Name: "Test Product", Slug: "test-product", Active: true}

	repo.On("GetProductByID", ctx, id).Return(expected, nil)

	product, err := svc.GetProduct(ctx, id)
	assert.NoError(t, err)
	assert.Equal(t, id, product.ID)
	repo.AssertExpectations(t)
}

func TestCatalogService_GetProductBySlug(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCatalogRepository)
	svc := NewCatalogService(repo)

	slug := "test-product"
	expected := &domain.Product{ID: uuid.NewString(), Name: "Test Product", Slug: slug, Active: true}

	repo.On("GetProductBySlug", ctx, slug).Return(expected, nil)

	product, err := svc.GetProductBySlug(ctx, slug)
	assert.NoError(t, err)
	assert.Equal(t, slug, product.Slug)
	repo.AssertExpectations(t)
}

func TestCatalogService_ListProductPrices(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCatalogRepository)
	svc := NewCatalogService(repo)

	productID := uuid.NewString()
	expected := []domain.ProductPrice{
		{ID: uuid.NewString(), ProductID: productID, Amount: 10000, Active: true},
		{ID: uuid.NewString(), ProductID: productID, Amount: 50000, Active: true},
	}

	repo.On("ListProductPrices", ctx, productID, true).Return(expected, nil)

	prices, err := svc.ListProductPrices(ctx, productID, true)
	assert.NoError(t, err)
	assert.Len(t, prices, 2)
	repo.AssertExpectations(t)
}

func TestCatalogService_GetProductPrice(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCatalogRepository)
	svc := NewCatalogService(repo)

	id := uuid.NewString()
	expected := &domain.ProductPrice{ID: id, Amount: 10000, Active: true}

	repo.On("GetProductPriceByID", ctx, id).Return(expected, nil)

	price, err := svc.GetProductPrice(ctx, id)
	assert.NoError(t, err)
	assert.Equal(t, id, price.ID)
	repo.AssertExpectations(t)
}
