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

type mockAdminProductRepository struct {
	mock.Mock
}

func (m *mockAdminProductRepository) ListProducts(ctx context.Context, filter ProductFilter, page, perPage int) ([]domain.Product, int64, error) {
	args := m.Called(ctx, filter, page, perPage)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]domain.Product), args.Get(1).(int64), args.Error(2)
}

func (m *mockAdminProductRepository) GetProductByID(ctx context.Context, id string) (*domain.Product, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Product), args.Error(1)
}

func (m *mockAdminProductRepository) GetProductBySlug(ctx context.Context, slug string) (*domain.Product, error) {
	args := m.Called(ctx, slug)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Product), args.Error(1)
}

func (m *mockAdminProductRepository) CreateProduct(ctx context.Context, p *domain.Product) error {
	args := m.Called(ctx, p)
	return args.Error(0)
}

func (m *mockAdminProductRepository) UpdateProduct(ctx context.Context, p *domain.Product) error {
	args := m.Called(ctx, p)
	return args.Error(0)
}

func (m *mockAdminProductRepository) DeleteProduct(ctx context.Context, id string, now time.Time) error {
	args := m.Called(ctx, id, now)
	return args.Error(0)
}

func (m *mockAdminProductRepository) GetProductPriceByID(ctx context.Context, id string) (*domain.ProductPrice, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ProductPrice), args.Error(1)
}

func (m *mockAdminProductRepository) CreateProductPrice(ctx context.Context, p *domain.ProductPrice) error {
	args := m.Called(ctx, p)
	return args.Error(0)
}

func (m *mockAdminProductRepository) UpdateProductPrice(ctx context.Context, p *domain.ProductPrice) error {
	args := m.Called(ctx, p)
	return args.Error(0)
}

func (m *mockAdminProductRepository) DeleteProductPrice(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func TestAdminProductService_ListProducts(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	expected := []domain.Product{
		{ID: uuid.NewString(), Name: "Product 1", Slug: "product-1", Active: true},
		{ID: uuid.NewString(), Name: "Product 2", Slug: "product-2", Active: false},
	}

	active := true
	categoryID := uuid.NewString()
	filter := ProductFilter{CategoryID: categoryID, Active: &active}
	repo.On("ListProducts", ctx, filter, 1, 20).Return(expected, int64(2), nil)

	products, total, err := svc.ListProducts(ctx, AdminProductFilter{CategoryID: categoryID, Active: &active}, 1, 20)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, products, 2)
	repo.AssertExpectations(t)
}

func TestAdminProductService_GetProduct(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	id := uuid.NewString()
	expected := &domain.Product{ID: id, Name: "Test Product", Slug: "test-product", Active: true}

	repo.On("GetProductByID", ctx, id).Return(expected, nil)

	product, err := svc.GetProduct(ctx, id)
	assert.NoError(t, err)
	assert.Equal(t, id, product.ID)
	repo.AssertExpectations(t)
}

func TestAdminProductService_GetProductBySlug(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	slug := "test-product"
	expected := &domain.Product{ID: uuid.NewString(), Name: "Test Product", Slug: slug, Active: true}

	repo.On("GetProductBySlug", ctx, slug).Return(expected, nil)

	product, err := svc.GetProductBySlug(ctx, slug)
	assert.NoError(t, err)
	assert.Equal(t, slug, product.Slug)
	repo.AssertExpectations(t)
}

func TestAdminProductService_CreateProduct(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	categoryID := uuid.NewString()
	name := "New Product"
	slug := "new-product"

	repo.On("GetProductBySlug", ctx, slug).Return(nil, ErrNotFound)
	repo.On("CreateProduct", ctx, mock.AnythingOfType("*domain.Product")).Return(nil).Run(func(args mock.Arguments) {
		product := args.Get(1).(*domain.Product)
		assert.Equal(t, categoryID, product.CategoryID)
		assert.Equal(t, name, product.Name)
		assert.Equal(t, slug, product.Slug)
		assert.True(t, product.Active)
	})

	product, err := svc.CreateProduct(ctx, categoryID, name, slug, "Description", "image.jpg")
	assert.NoError(t, err)
	assert.Equal(t, name, product.Name)
	assert.Equal(t, slug, product.Slug)
	repo.AssertExpectations(t)
}

func TestAdminProductService_CreateProduct_DuplicateSlug(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	slug := "existing-product"
	existing := &domain.Product{ID: uuid.NewString(), Slug: slug}

	repo.On("GetProductBySlug", ctx, slug).Return(existing, nil)

	_, err := svc.CreateProduct(ctx, uuid.NewString(), "Name", slug, "", "")
	assert.Error(t, err)
	assert.Equal(t, ErrConflict, err)
	repo.AssertExpectations(t)
}

func TestAdminProductService_UpdateProduct(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	id := uuid.NewString()
	existing := &domain.Product{ID: id, Name: "Old", Slug: "old", CategoryID: uuid.NewString()}

	repo.On("GetProductByID", ctx, id).Return(existing, nil)
	repo.On("GetProductBySlug", ctx, "new-slug").Return(nil, ErrNotFound)
	repo.On("UpdateProduct", ctx, mock.AnythingOfType("*domain.Product")).Return(nil).Run(func(args mock.Arguments) {
		product := args.Get(1).(*domain.Product)
		assert.Equal(t, "New Name", product.Name)
		assert.Equal(t, "new-slug", product.Slug)
		assert.False(t, product.Active)
	})

	active := false
	product, err := svc.UpdateProduct(ctx, id, "", "New Name", "new-slug", "", "", &active)
	assert.NoError(t, err)
	assert.Equal(t, "New Name", product.Name)
	assert.Equal(t, "new-slug", product.Slug)
	assert.False(t, product.Active)
	repo.AssertExpectations(t)
}

func TestAdminProductService_DeleteProduct(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	id := uuid.NewString()

	repo.On("DeleteProduct", ctx, id, mock.AnythingOfType("time.Time")).Return(nil)

	err := svc.DeleteProduct(ctx, id)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestAdminProductService_CreateProductPrice(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	productID := uuid.NewString()
	durationID := uuid.NewString()
	amount := int64(50000)

	repo.On("CreateProductPrice", ctx, mock.AnythingOfType("*domain.ProductPrice")).Return(nil).Run(func(args mock.Arguments) {
		price := args.Get(1).(*domain.ProductPrice)
		assert.Equal(t, productID, price.ProductID)
		assert.Equal(t, durationID, price.DurationID)
		assert.Equal(t, amount, price.Amount)
		assert.Equal(t, "IDR", price.Currency)
		assert.True(t, price.Active)
	})

	price, err := svc.CreateProductPrice(ctx, productID, durationID, amount, "IDR")
	assert.NoError(t, err)
	assert.Equal(t, productID, price.ProductID)
	assert.Equal(t, durationID, price.DurationID)
	assert.Equal(t, amount, price.Amount)
	repo.AssertExpectations(t)
}

func TestAdminProductService_UpdateProductPrice(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	id := uuid.NewString()
	existing := &domain.ProductPrice{ID: id, Amount: 10000, Active: true}

	repo.On("GetProductPriceByID", ctx, id).Return(existing, nil)
	repo.On("UpdateProductPrice", ctx, mock.AnythingOfType("*domain.ProductPrice")).Return(nil).Run(func(args mock.Arguments) {
		price := args.Get(1).(*domain.ProductPrice)
		assert.Equal(t, int64(15000), price.Amount)
		assert.False(t, price.Active)
	})

	newAmount := int64(15000)
	newActive := false
	price, err := svc.UpdateProductPrice(ctx, id, &newAmount, &newActive)
	assert.NoError(t, err)
	assert.Equal(t, int64(15000), price.Amount)
	assert.False(t, price.Active)
	repo.AssertExpectations(t)
}

func TestAdminProductService_DeleteProductPrice(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	id := uuid.NewString()

	repo.On("DeleteProductPrice", ctx, id).Return(nil)

	err := svc.DeleteProductPrice(ctx, id)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}
