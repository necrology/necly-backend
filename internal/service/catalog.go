package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
)

type CatalogRepository interface {
	ListCategories(ctx context.Context, activeOnly bool, page, perPage int) ([]domain.Category, int64, error)
	GetCategoryByID(ctx context.Context, id string) (*domain.Category, error)
	GetCategoryBySlug(ctx context.Context, slug string) (*domain.Category, error)
	CreateCategory(ctx context.Context, c *domain.Category) error
	UpdateCategory(ctx context.Context, c *domain.Category) error
	DeleteCategory(ctx context.Context, id string, now time.Time) error
	ListCategoriesWithFilter(ctx context.Context, filter CategoryFilter, page, perPage int) ([]domain.Category, int64, error)

	ListProducts(ctx context.Context, filter ProductFilter, page, perPage int) ([]domain.Product, int64, error)
	GetProductByID(ctx context.Context, id string) (*domain.Product, error)
	GetProductBySlug(ctx context.Context, slug string) (*domain.Product, error)
	CreateProduct(ctx context.Context, p *domain.Product) error
	UpdateProduct(ctx context.Context, p *domain.Product) error
	DeleteProduct(ctx context.Context, id string, now time.Time) error

	ListProductPrices(ctx context.Context, productID string, activeOnly bool) ([]domain.ProductPrice, error)
	GetProductPriceByID(ctx context.Context, id string) (*domain.ProductPrice, error)
	CreateProductPrice(ctx context.Context, p *domain.ProductPrice) error
	UpdateProductPrice(ctx context.Context, p *domain.ProductPrice) error
	DeleteProductPrice(ctx context.Context, id string) error

	// ListProductsWithStock returns products with prices including available slot counts
	ListProductsWithStock(ctx context.Context, filter ProductFilter, page, perPage int) ([]domain.ProductWithStock, int64, error)
	GetProductWithStock(ctx context.Context, id string) (*domain.ProductWithStock, error)
	GetProductBySlugWithStock(ctx context.Context, slug string) (*domain.ProductWithStock, error)
}

// ProductFilter is the single, validated catalog filter shared by the public
// and admin list endpoints. A nil Active returns both states (admin); a public
// caller always sets it to true so inactive products never leak.
type ProductFilter struct {
	CategoryID string
	Search     string
	Active     *bool
	// MinPrice and MaxPrice are inclusive bounds on at least one active price.
	MinPrice *int64
	MaxPrice *int64
	// InStock keeps only products with at least one available slot on an
	// active price.
	InStock bool
}

// ValidateCategoryID rejects display names, slugs and malformed values before
// they reach a UUID column. Postgres otherwise answers SQLSTATE 22P02, which
// the API used to surface as a 500.
func ValidateCategoryID(id string) error {
	if id == "" {
		return nil
	}
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("%w: category harus berupa UUID kategori yang valid", ErrInvalidInput)
	}
	return nil
}

func ValidateProductFilter(filter ProductFilter) error {
	if err := ValidateCategoryID(filter.CategoryID); err != nil {
		return err
	}
	if filter.MinPrice != nil && *filter.MinPrice < 0 {
		return fmt.Errorf("%w: min_price tidak boleh negatif", ErrInvalidInput)
	}
	if filter.MaxPrice != nil && *filter.MaxPrice < 0 {
		return fmt.Errorf("%w: max_price tidak boleh negatif", ErrInvalidInput)
	}
	if filter.MinPrice != nil && filter.MaxPrice != nil && *filter.MinPrice > *filter.MaxPrice {
		return fmt.Errorf("%w: min_price tidak boleh lebih besar dari max_price", ErrInvalidInput)
	}
	return nil
}

type CatalogService struct {
	repo CatalogRepository
}

func NewCatalogService(repo CatalogRepository) *CatalogService {
	return &CatalogService{repo: repo}
}

func (s *CatalogService) ListCategories(ctx context.Context, activeOnly bool, page, perPage int) ([]domain.Category, int64, error) {
	return s.repo.ListCategories(ctx, activeOnly, page, perPage)
}

func (s *CatalogService) GetCategory(ctx context.Context, id string) (*domain.Category, error) {
	return s.repo.GetCategoryByID(ctx, id)
}

func (s *CatalogService) GetCategoryBySlug(ctx context.Context, slug string) (*domain.Category, error) {
	return s.repo.GetCategoryBySlug(ctx, slug)
}

func (s *CatalogService) ListProducts(ctx context.Context, filter ProductFilter, page, perPage int) ([]domain.Product, int64, error) {
	if err := ValidateProductFilter(filter); err != nil {
		return nil, 0, err
	}
	return s.repo.ListProducts(ctx, filter, page, perPage)
}

func (s *CatalogService) GetProduct(ctx context.Context, id string) (*domain.Product, error) {
	return s.repo.GetProductByID(ctx, id)
}

func (s *CatalogService) GetProductBySlug(ctx context.Context, slug string) (*domain.Product, error) {
	return s.repo.GetProductBySlug(ctx, slug)
}

func (s *CatalogService) ListProductPrices(ctx context.Context, productID string, activeOnly bool) ([]domain.ProductPrice, error) {
	return s.repo.ListProductPrices(ctx, productID, activeOnly)
}

func (s *CatalogService) GetProductPrice(ctx context.Context, id string) (*domain.ProductPrice, error) {
	return s.repo.GetProductPriceByID(ctx, id)
}

func (s *CatalogService) ListProductsWithStock(ctx context.Context, filter ProductFilter, page, perPage int) ([]domain.ProductWithStock, int64, error) {
	if err := ValidateProductFilter(filter); err != nil {
		return nil, 0, err
	}
	return s.repo.ListProductsWithStock(ctx, filter, page, perPage)
}

func (s *CatalogService) GetProductWithStock(ctx context.Context, id string) (*domain.ProductWithStock, error) {
	return s.repo.GetProductWithStock(ctx, id)
}

func (s *CatalogService) GetProductBySlugWithStock(ctx context.Context, slug string) (*domain.ProductWithStock, error) {
	return s.repo.GetProductBySlugWithStock(ctx, slug)
}
