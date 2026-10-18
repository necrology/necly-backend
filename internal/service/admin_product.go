package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
)

type AdminProductService struct {
	repo AdminProductRepository
}

func NewAdminProductService(repo AdminProductRepository) *AdminProductService {
	return &AdminProductService{repo: repo}
}

func (s *AdminProductService) ListProducts(ctx context.Context, filter AdminProductFilter, page, perPage int) ([]domain.Product, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	repoFilter := ProductFilter{
		CategoryID: filter.CategoryID,
		Search:     filter.Search,
		// Keep the tri-state: nil means "all products", not "inactive only".
		Active: filter.Active,
	}
	if err := ValidateProductFilter(repoFilter); err != nil {
		return nil, 0, err
	}
	return s.repo.ListProducts(ctx, repoFilter, page, perPage)
}

func (s *AdminProductService) GetProduct(ctx context.Context, id string) (*domain.Product, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	return s.repo.GetProductByID(ctx, id)
}

func (s *AdminProductService) GetProductBySlug(ctx context.Context, slug string) (*domain.Product, error) {
	return s.repo.GetProductBySlug(ctx, slug)
}

func (s *AdminProductService) CreateProduct(ctx context.Context, categoryID, name, slug, description, imageURL string) (*domain.Product, error) {
	if categoryID == "" || name == "" || slug == "" {
		return nil, ErrInvalidInput
	}
	if err := ValidateCategoryID(categoryID); err != nil {
		return nil, err
	}

	// Check if slug already exists
	existing, err := s.repo.GetProductBySlug(ctx, slug)
	if err == nil && existing != nil {
		return nil, ErrConflict
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("check existing slug: %w", err)
	}

	now := time.Now().UTC()
	product := &domain.Product{
		ID:          uuid.NewString(),
		CategoryID:  categoryID,
		Name:        name,
		Slug:        strings.ToLower(slug),
		Description: description,
		ImageURL:    imageURL,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.repo.CreateProduct(ctx, product); err != nil {
		return nil, fmt.Errorf("create product: %w", err)
	}
	return product, nil
}

func (s *AdminProductService) UpdateProduct(ctx context.Context, id, categoryID, name, slug, description, imageURL string, active *bool) (*domain.Product, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	product, err := s.repo.GetProductByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if categoryID != "" {
		if err := ValidateCategoryID(categoryID); err != nil {
			return nil, err
		}
		product.CategoryID = categoryID
	}
	if name != "" {
		product.Name = name
	}
	if slug != "" {
		// Check if new slug is already taken
		existing, err := s.repo.GetProductBySlug(ctx, slug)
		if err == nil && existing != nil && existing.ID != id {
			return nil, ErrConflict
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("check existing slug: %w", err)
		}
		product.Slug = strings.ToLower(slug)
	}
	if description != "" {
		product.Description = description
	}
	if imageURL != "" {
		product.ImageURL = imageURL
	}
	if active != nil {
		product.Active = *active
	}

	product.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateProduct(ctx, product); err != nil {
		return nil, fmt.Errorf("update product: %w", err)
	}
	return product, nil
}

func (s *AdminProductService) DeleteProduct(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	now := time.Now().UTC()
	return s.repo.DeleteProduct(ctx, id, now)
}

func (s *AdminProductService) CreateProductPrice(ctx context.Context, productID, durationID string, amount int64, currency string) (*domain.ProductPrice, error) {
	if productID == "" || durationID == "" || amount < 0 {
		return nil, ErrInvalidInput
	}
	if _, err := uuid.Parse(productID); err != nil {
		return nil, ErrInvalidInput
	}
	if _, err := uuid.Parse(durationID); err != nil {
		return nil, ErrInvalidInput
	}

	now := time.Now().UTC()
	price := &domain.ProductPrice{
		ID:         uuid.NewString(),
		ProductID:  productID,
		DurationID: durationID,
		Amount:     amount,
		Currency:   currency,
		Active:     true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := s.repo.CreateProductPrice(ctx, price); err != nil {
		return nil, fmt.Errorf("create product price: %w", err)
	}
	return price, nil
}

func (s *AdminProductService) UpdateProductPrice(ctx context.Context, id string, amount *int64, active *bool) (*domain.ProductPrice, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	price, err := s.repo.GetProductPriceByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if amount != nil {
		if *amount < 0 {
			return nil, ErrInvalidInput
		}
		price.Amount = *amount
	}
	if active != nil {
		price.Active = *active
	}

	price.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateProductPrice(ctx, price); err != nil {
		return nil, fmt.Errorf("update product price: %w", err)
	}
	return price, nil
}

func (s *AdminProductService) DeleteProductPrice(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	return s.repo.DeleteProductPrice(ctx, id)
}
