package service

import (
	"context"
	"errors"
	"strings"
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

	product, err := svc.CreateProduct(ctx, categoryID, name, slug, "Description", "image.jpg", "", false)
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

	_, err := svc.CreateProduct(ctx, uuid.NewString(), "Name", slug, "", "", "", false)
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
	product, err := svc.UpdateProduct(ctx, id, "", "New Name", "new-slug", "", nil, nil, &active, nil)
	assert.NoError(t, err)
	assert.Equal(t, "New Name", product.Name)
	assert.Equal(t, "new-slug", product.Slug)
	assert.False(t, product.Active)
	repo.AssertExpectations(t)
}

func TestAdminProductService_CreateProduct_ComingSoon(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	categoryID := uuid.NewString()
	slug := "coming-soon-product"

	repo.On("GetProductBySlug", ctx, slug).Return(nil, ErrNotFound)
	repo.On("CreateProduct", ctx, mock.AnythingOfType("*domain.Product")).Return(nil).Run(func(args mock.Arguments) {
		product := args.Get(1).(*domain.Product)
		assert.True(t, product.ComingSoon)
		assert.True(t, product.Active)
		assert.Equal(t, "https://cdn.example.test/coming-soon.jpg", product.ImageURL)
	})

	product, err := svc.CreateProduct(ctx, categoryID, "Coming Soon", slug, "", "https://cdn.example.test/coming-soon.jpg", "", true)
	assert.NoError(t, err)
	assert.True(t, product.ComingSoon)
	repo.AssertExpectations(t)
}

func TestAdminProductService_UpdateProduct_PointerFields(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	id := uuid.NewString()
	existing := &domain.Product{ID: id, Name: "Old", Slug: "old", CategoryID: uuid.NewString(), Active: true, ImageURL: "old.jpg"}

	repo.On("GetProductByID", ctx, id).Return(existing, nil)
	repo.On("UpdateProduct", ctx, mock.AnythingOfType("*domain.Product")).Return(nil).Run(func(args mock.Arguments) {
		product := args.Get(1).(*domain.Product)
		assert.Equal(t, "new.jpg", product.ImageURL)
		assert.True(t, product.ComingSoon)
		assert.True(t, product.Active)
	})

	newImage := "new.jpg"
	comingSoon := true
	product, err := svc.UpdateProduct(ctx, id, "", "", "", "", &newImage, nil, nil, &comingSoon)
	assert.NoError(t, err)
	assert.Equal(t, "new.jpg", product.ImageURL)
	assert.True(t, product.ComingSoon)
	assert.True(t, product.Active)
	repo.AssertExpectations(t)
}

func TestAdminProductService_UpdateProduct_ClearsImageAndPreservesOmittedFields(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	id := uuid.NewString()
	existing := &domain.Product{ID: id, Name: "Old", Slug: "old", CategoryID: uuid.NewString(), Active: true, ImageURL: "old.jpg", ComingSoon: true}

	repo.On("GetProductByID", ctx, id).Return(existing, nil)
	repo.On("UpdateProduct", ctx, mock.AnythingOfType("*domain.Product")).Return(nil).Run(func(args mock.Arguments) {
		product := args.Get(1).(*domain.Product)
		assert.Equal(t, "", product.ImageURL)
		assert.True(t, product.ComingSoon)
		assert.True(t, product.Active)
		assert.Equal(t, "Old", product.Name)
	})

	empty := ""
	product, err := svc.UpdateProduct(ctx, id, "", "", "", "", &empty, nil, nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, "", product.ImageURL)
	assert.True(t, product.ComingSoon)
	assert.True(t, product.Active)
	repo.AssertExpectations(t)
}

func TestAdminProductService_UpdateProduct_RejectsComingSoonOnInactiveProduct(t *testing.T) {
	cases := []struct {
		name       string
		existing   *domain.Product
		active     *bool
		comingSoon *bool
	}{
		{
			name:       "only coming_soon on already inactive product",
			existing:   &domain.Product{Active: false, ComingSoon: false},
			comingSoon: boolPtr(true),
		},
		{
			name:       "coming_soon and active=false together",
			existing:   &domain.Product{Active: true, ComingSoon: false},
			active:     boolPtr(false),
			comingSoon: boolPtr(true),
		},
		{
			name:     "deactivating a coming soon product",
			existing: &domain.Product{Active: true, ComingSoon: true},
			active:   boolPtr(false),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			repo := new(mockAdminProductRepository)
			svc := NewAdminProductService(repo)

			id := uuid.NewString()
			existing := *testCase.existing
			existing.ID = id
			existing.Slug = "existing"
			existing.CategoryID = uuid.NewString()
			repo.On("GetProductByID", ctx, id).Return(&existing, nil)

			_, err := svc.UpdateProduct(ctx, id, "", "", "", "", nil, nil, testCase.active, testCase.comingSoon)
			assert.ErrorIs(t, err, ErrInvalidInput)
			repo.AssertNotCalled(t, "UpdateProduct", mock.Anything, mock.Anything)
			repo.AssertExpectations(t)
		})
	}
}

func TestAdminProductService_CreateProduct_IconURL(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	categoryID := uuid.NewString()
	slug := "netflix-premium"
	repo.On("GetProductBySlug", ctx, slug).Return(nil, ErrNotFound)
	repo.On("CreateProduct", ctx, mock.AnythingOfType("*domain.Product")).Return(nil).Run(func(args mock.Arguments) {
		product := args.Get(1).(*domain.Product)
		assert.Equal(t, "/design-assets/logo-netflix.png", product.IconURL)
	})

	product, err := svc.CreateProduct(ctx, categoryID, "Netflix Premium", slug, "", "", "  /design-assets/logo-netflix.png  ", false)
	assert.NoError(t, err)
	assert.Equal(t, "/design-assets/logo-netflix.png", product.IconURL)
	repo.AssertExpectations(t)
}

func TestAdminProductService_RejectsInvalidIconURL(t *testing.T) {
	cases := []string{
		"javascript:alert(1)",
		"data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=",
		"data:text/html;base64,PGgxPmhpPC9oMT4=",
		"data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==",
		"data:image/png;base64,not-base64!",
		"ftp://cdn.example.test/logo.png",
		"design-assets/logo-netflix.png",
		"//cdn.example.test/logo-netflix.png",
		"https://",
	}
	for _, icon := range cases {
		t.Run(icon, func(t *testing.T) {
			ctx := context.Background()
			repo := new(mockAdminProductRepository)
			svc := NewAdminProductService(repo)

			_, err := svc.CreateProduct(ctx, uuid.NewString(), "Name", "new-product", "", "", icon, false)
			assert.ErrorIs(t, err, ErrInvalidInput)
			repo.AssertNotCalled(t, "CreateProduct", mock.Anything, mock.Anything)

			id := uuid.NewString()
			existing := &domain.Product{ID: id, Slug: "existing", CategoryID: uuid.NewString(), Active: true}
			repo.On("GetProductByID", ctx, id).Return(existing, nil)
			_, err = svc.UpdateProduct(ctx, id, "", "", "", "", nil, &icon, nil, nil)
			assert.ErrorIs(t, err, ErrInvalidInput)
			repo.AssertNotCalled(t, "UpdateProduct", mock.Anything, mock.Anything)
		})
	}
}

func TestAdminProductService_UpdateProduct_IconURLPointerSemantics(t *testing.T) {
	ctx := context.Background()

	t.Run("omitted preserves the stored icon", func(t *testing.T) {
		repo := new(mockAdminProductRepository)
		svc := NewAdminProductService(repo)

		id := uuid.NewString()
		existing := &domain.Product{ID: id, Slug: "existing", CategoryID: uuid.NewString(), Active: true, IconURL: "/design-assets/logo-netflix.png"}
		repo.On("GetProductByID", ctx, id).Return(existing, nil)
		repo.On("UpdateProduct", ctx, mock.AnythingOfType("*domain.Product")).Return(nil).Run(func(args mock.Arguments) {
			assert.Equal(t, "/design-assets/logo-netflix.png", args.Get(1).(*domain.Product).IconURL)
		})

		product, err := svc.UpdateProduct(ctx, id, "", "", "", "", nil, nil, nil, nil)
		assert.NoError(t, err)
		assert.Equal(t, "/design-assets/logo-netflix.png", product.IconURL)
		repo.AssertExpectations(t)
	})

	t.Run("empty string clears the icon", func(t *testing.T) {
		repo := new(mockAdminProductRepository)
		svc := NewAdminProductService(repo)

		id := uuid.NewString()
		existing := &domain.Product{ID: id, Slug: "existing", CategoryID: uuid.NewString(), Active: true, IconURL: "/design-assets/logo-netflix.png"}
		repo.On("GetProductByID", ctx, id).Return(existing, nil)
		repo.On("UpdateProduct", ctx, mock.AnythingOfType("*domain.Product")).Return(nil).Run(func(args mock.Arguments) {
			assert.Equal(t, "", args.Get(1).(*domain.Product).IconURL)
		})

		empty := "   "
		product, err := svc.UpdateProduct(ctx, id, "", "", "", "", nil, &empty, nil, nil)
		assert.NoError(t, err)
		assert.Equal(t, "", product.IconURL)
		repo.AssertExpectations(t)
	})
}

func TestAdminProductService_AcceptsRasterIconDataURL(t *testing.T) {
	cases := []struct {
		name string
		icon string
	}{
		{"webp", "data:image/webp;base64,UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEAAUAmJaQAA3AA/vuUAAA="},
		{"png", "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAFgwJ/lYQ5SAAAAABJRU5ErkJggg=="},
		{" jpeg with spaces ", "  data:image/jpeg;base64,/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAP//////////////////////////////////////////////////////////////////////////////////////2wBDAf//////////////////////////////////////////////////////////////////////////////////////wAARCAABAAEDASIAAhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAf/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/9oADAMBAAIQAxAAAAF//8QAFBABAAAAAAAAAAAAAAAAAAAAAP/aAAgBAQABBQJ//8QAFBEBAAAAAAAAAAAAAAAAAAAAAP/aAAgBAwEBPwF//8QAFBEBAAAAAAAAAAAAAAAAAAAAAP/aAAgBAgEBPwF//8QAFBABAAAAAAAAAAAAAAAAAAAAAP/aAAgBAQAGPwJ//8QAFBABAAAAAAAAAAAAAAAAAAAAAP/aAAgBAQABPyF//9oADAMBAAIAAwAAABD/xAAUEQEAAAAAAAAAAAAAAAAAAAAA/9oACAEDAQE/EH//xAAUEQEAAAAAAAAAAAAAAAAAAAAA/9oACAECAQE/EH//xAAUEAEAAAAAAAAAAAAAAAAAAAAA/9oACAEBAAE/EH//2Q==  "},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repo := new(mockAdminProductRepository)
			svc := NewAdminProductService(repo)
			slug := "icon-data-url"
			repo.On("GetProductBySlug", context.Background(), slug).Return(nil, ErrNotFound)
			repo.On("CreateProduct", context.Background(), mock.AnythingOfType("*domain.Product")).Return(nil).Run(func(args mock.Arguments) {
				assert.Equal(t, strings.TrimSpace(testCase.icon), args.Get(1).(*domain.Product).IconURL)
			})

			product, err := svc.CreateProduct(context.Background(), uuid.NewString(), "Name", slug, "", "", testCase.icon, false)
			assert.NoError(t, err)
			assert.Equal(t, strings.TrimSpace(testCase.icon), product.IconURL)
			repo.AssertExpectations(t)
		})
	}
}

func TestAdminProductService_IconDataURLHas512KiBLimit(t *testing.T) {
	prefix := "data:image/png;base64,"
	// Keep the complete data URL at or below the limit. The payload length is
	// a multiple of four so the base64 decoder accepts it.
	under := prefix + strings.Repeat("A", maxProductIconDataURLLength-len(prefix)-2)
	if len(under) > maxProductIconDataURLLength {
		t.Fatalf("valid fixture is %d bytes, limit is %d", len(under), maxProductIconDataURLLength)
	}
	over := prefix + strings.Repeat("A", maxProductIconDataURLLength-len(prefix)+4)

	if _, err := normalizeProductIconURL(under); err != nil {
		t.Fatalf("data URL under the limit was rejected: %v", err)
	}
	if _, err := normalizeProductIconURL(over); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("data URL over the limit yielded %v, want ErrInvalidInput", err)
	}
}

func boolPtr(value bool) *bool { return &value }

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
