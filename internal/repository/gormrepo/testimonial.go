package gormrepo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
	"gorm.io/gorm"
)

type TestimonialRepository interface {
	ListTestimonials(ctx context.Context, filter service.TestimonialFilter, page, perPage int) ([]domain.Testimonial, int64, error)
	GetTestimonial(ctx context.Context, id string) (*domain.Testimonial, error)
	GetUserTestimonials(ctx context.Context, userID string, page, perPage int) ([]domain.Testimonial, int64, error)
	CreateTestimonial(ctx context.Context, t *domain.Testimonial) error
	UpdateTestimonial(ctx context.Context, t *domain.Testimonial) error
	DeleteTestimonial(ctx context.Context, id string) error
	ModerateTestimonial(ctx context.Context, id, moderatorID, status string, now time.Time) error
}

func (s *Store) ListTestimonials(ctx context.Context, filter service.TestimonialFilter, page, perPage int) ([]domain.Testimonial, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	query := s.db.WithContext(ctx).Model(&domain.Testimonial{}).Preload("Reviewer").Preload("Product")
	if filter.ProductID != "" {
		query = query.Where("product_id = ?", filter.ProductID)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Rating > 0 {
		query = query.Where("rating = ?", filter.Rating)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count testimonials: %w", err)
	}
	var testimonials []domain.Testimonial
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC").Offset(offset).Limit(perPage).Find(&testimonials).Error; err != nil {
		return nil, 0, fmt.Errorf("list testimonials: %w", err)
	}
	return testimonials, total, nil
}

func (s *Store) GetTestimonial(ctx context.Context, id string) (*domain.Testimonial, error) {
	var t domain.Testimonial
	err := s.db.WithContext(ctx).Preload("Reviewer").Preload("Product").Where("id = ?", id).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &t, err
}

func (s *Store) GetUserTestimonials(ctx context.Context, userID string, page, perPage int) ([]domain.Testimonial, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	query := s.db.WithContext(ctx).Model(&domain.Testimonial{}).Preload("Product").Where("user_id = ?", userID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count testimonials: %w", err)
	}
	var testimonials []domain.Testimonial
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC").Offset(offset).Limit(perPage).Find(&testimonials).Error; err != nil {
		return nil, 0, fmt.Errorf("list testimonials: %w", err)
	}
	return testimonials, total, nil
}

func (s *Store) CreateTestimonial(ctx context.Context, t *domain.Testimonial) error {
	t.ID = uuid.NewString()
	t.Status = "pending"
	t.CreatedAt = s.nowUTC()
	t.UpdatedAt = t.CreatedAt
	return s.db.WithContext(ctx).Create(t).Error
}

func (s *Store) UpdateTestimonial(ctx context.Context, t *domain.Testimonial) error {
	t.UpdatedAt = s.nowUTC()
	return s.db.WithContext(ctx).Where("id = ? AND user_id = ?", t.ID, t.UserID).Save(t).Error
}

func (s *Store) DeleteTestimonial(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&domain.Testimonial{}, "id = ?", id).Error
}

func (s *Store) ModerateTestimonial(ctx context.Context, id, moderatorID, status string, now time.Time) error {
	valid := map[string]bool{"approved": true, "rejected": true, "pending": true}
	if !valid[status] {
		return service.ErrInvalidInput
	}
	result := s.db.WithContext(ctx).Model(&domain.Testimonial{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": status, "moderator_id": moderatorID, "moderated_at": now, "updated_at": now})
	if result.Error != nil {
		return fmt.Errorf("moderate testimonial: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return service.ErrNotFound
	}
	return nil
}
