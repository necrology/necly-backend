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

type AssetRepository interface {
	ListAssets(ctx context.Context, userID string, page, perPage int) ([]domain.Asset, int64, error)
	GetAsset(ctx context.Context, id string) (*domain.Asset, error)
	CreateAsset(ctx context.Context, a *domain.Asset) error
	UpdateAsset(ctx context.Context, a *domain.Asset) error
	DeleteAsset(ctx context.Context, id string) error
	MarkAssetUploaded(ctx context.Context, id string, now time.Time) error
}

func (s *Store) ListAssets(ctx context.Context, userID string, page, perPage int) ([]domain.Asset, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	query := s.db.WithContext(ctx).Model(&domain.Asset{}).Where("user_id = ?", userID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count assets: %w", err)
	}
	var assets []domain.Asset
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC").Offset(offset).Limit(perPage).Find(&assets).Error; err != nil {
		return nil, 0, fmt.Errorf("list assets: %w", err)
	}
	return assets, total, nil
}

func (s *Store) GetAsset(ctx context.Context, id string) (*domain.Asset, error) {
	var a domain.Asset
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &a, err
}

func (s *Store) CreateAsset(ctx context.Context, a *domain.Asset) error {
	a.ID = uuid.NewString()
	a.Status = "pending"
	a.CreatedAt = s.nowUTC()
	a.UpdatedAt = a.CreatedAt
	return s.db.WithContext(ctx).Create(a).Error
}

func (s *Store) UpdateAsset(ctx context.Context, a *domain.Asset) error {
	a.UpdatedAt = s.nowUTC()
	return s.db.WithContext(ctx).Where("id = ? AND user_id = ?", a.ID, a.UserID).Save(a).Error
}

func (s *Store) DeleteAsset(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&domain.Asset{}, "id = ?", id).Error
}

func (s *Store) MarkAssetUploaded(ctx context.Context, id string, now time.Time) error {
	result := s.db.WithContext(ctx).Model(&domain.Asset{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": "completed", "uploaded_at": now, "updated_at": now})
	if result.Error != nil {
		return fmt.Errorf("mark asset uploaded: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return service.ErrNotFound
	}
	return nil
}
