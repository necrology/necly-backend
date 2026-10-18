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

type AdminCategoryService struct {
	repo AdminCategoryRepository
}

func NewAdminCategoryService(repo AdminCategoryRepository) *AdminCategoryService {
	return &AdminCategoryService{repo: repo}
}

func (s *AdminCategoryService) ListCategories(ctx context.Context, filter CategoryFilter, page, perPage int) ([]domain.Category, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	return s.repo.ListCategoriesWithFilter(ctx, filter, page, perPage)
}

func (s *AdminCategoryService) GetCategory(ctx context.Context, id string) (*domain.Category, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	return s.repo.GetCategoryByID(ctx, id)
}

func (s *AdminCategoryService) GetCategoryBySlug(ctx context.Context, slug string) (*domain.Category, error) {
	return s.repo.GetCategoryBySlug(ctx, slug)
}

func (s *AdminCategoryService) CreateCategory(ctx context.Context, name, slug, description, imageURL string) (*domain.Category, error) {
	if name == "" || slug == "" {
		return nil, ErrInvalidInput
	}

	// Check if slug already exists
	existing, err := s.repo.GetCategoryBySlug(ctx, slug)
	if err == nil && existing != nil {
		return nil, ErrConflict
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("check existing slug: %w", err)
	}

	now := time.Now().UTC()
	category := &domain.Category{
		ID:          uuid.NewString(),
		Name:        name,
		Slug:        strings.ToLower(slug),
		Description: description,
		ImageURL:    imageURL,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.repo.CreateCategory(ctx, category); err != nil {
		return nil, fmt.Errorf("create category: %w", err)
	}
	return category, nil
}

func (s *AdminCategoryService) UpdateCategory(ctx context.Context, id, name, slug, description, imageURL string, active *bool) (*domain.Category, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	category, err := s.repo.GetCategoryByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if name != "" {
		category.Name = name
	}
	if slug != "" {
		// Check if new slug is already taken
		existing, err := s.repo.GetCategoryBySlug(ctx, slug)
		if err == nil && existing != nil && existing.ID != id {
			return nil, ErrConflict
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("check existing slug: %w", err)
		}
		category.Slug = strings.ToLower(slug)
	}
	if description != "" {
		category.Description = description
	}
	if imageURL != "" {
		category.ImageURL = imageURL
	}
	if active != nil {
		category.Active = *active
	}

	category.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateCategory(ctx, category); err != nil {
		return nil, fmt.Errorf("update category: %w", err)
	}
	return category, nil
}

func (s *AdminCategoryService) DeleteCategory(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	now := time.Now().UTC()
	return s.repo.DeleteCategory(ctx, id, now)
}
