package service

import (
	"context"
	"time"

	"github.com/necly/services/internal/domain"
)

type AdminProductRepository interface {
	ListProducts(ctx context.Context, filter ProductFilter, page, perPage int) ([]domain.Product, int64, error)
	GetProductByID(ctx context.Context, id string) (*domain.Product, error)
	GetProductBySlug(ctx context.Context, slug string) (*domain.Product, error)
	CreateProduct(ctx context.Context, p *domain.Product) error
	UpdateProduct(ctx context.Context, p *domain.Product) error
	DeleteProduct(ctx context.Context, id string, now time.Time) error
	GetProductPriceByID(ctx context.Context, id string) (*domain.ProductPrice, error)
	CreateProductPrice(ctx context.Context, p *domain.ProductPrice) error
	UpdateProductPrice(ctx context.Context, p *domain.ProductPrice) error
	DeleteProductPrice(ctx context.Context, id string) error
}

type AdminProductFilter struct {
	CategoryID string
	Active     *bool
	Search     string
}
