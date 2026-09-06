package service

import (
	"context"
	"time"

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

type ProductFilter struct {
	CategoryID string
	Search     string
	ActiveOnly bool
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

func (s *CatalogService) ListProducts(ctx context.Context, categoryID, search string, activeOnly bool, page, perPage int) ([]domain.Product, int64, error) {
	filter := ProductFilter{
		CategoryID: categoryID,
		Search:     search,
		ActiveOnly: activeOnly,
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

func (s *CatalogService) ListProductsWithStock(ctx context.Context, categoryID, search string, activeOnly bool, page, perPage int) ([]domain.ProductWithStock, int64, error) {
	filter := ProductFilter{
		CategoryID: categoryID,
		Search:     search,
		ActiveOnly: activeOnly,
	}
	return s.repo.ListProductsWithStock(ctx, filter, page, perPage)
}

func (s *CatalogService) GetProductWithStock(ctx context.Context, id string) (*domain.ProductWithStock, error) {
	return s.repo.GetProductWithStock(ctx, id)
}

func (s *CatalogService) GetProductBySlugWithStock(ctx context.Context, slug string) (*domain.ProductWithStock, error) {
	return s.repo.GetProductBySlugWithStock(ctx, slug)
}
