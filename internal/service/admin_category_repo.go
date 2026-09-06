package service

import (
	"context"
	"time"

	"github.com/necly/services/internal/domain"
)

type CategoryFilter struct {
	Active *bool
	Search string
}

type AdminCategoryRepository interface {
	ListCategoriesWithFilter(ctx context.Context, filter CategoryFilter, page, perPage int) ([]domain.Category, int64, error)
	GetCategoryByID(ctx context.Context, id string) (*domain.Category, error)
	GetCategoryBySlug(ctx context.Context, slug string) (*domain.Category, error)
	CreateCategory(ctx context.Context, c *domain.Category) error
	UpdateCategory(ctx context.Context, c *domain.Category) error
	DeleteCategory(ctx context.Context, id string, now time.Time) error
}
