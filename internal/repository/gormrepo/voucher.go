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
	"gorm.io/gorm/clause"
)

type VoucherRepository interface {
	ListVouchers(ctx context.Context, filter service.VoucherFilter, page, perPage int) ([]domain.Voucher, int64, error)
	GetVoucherByID(ctx context.Context, id string) (*domain.Voucher, error)
	GetVoucherByCode(ctx context.Context, code string) (*domain.Voucher, error)
	CreateVoucher(ctx context.Context, v *domain.Voucher) error
	UpdateVoucher(ctx context.Context, v *domain.Voucher) error
	DeleteVoucher(ctx context.Context, id string, now time.Time) error
	CountVoucherRedemptions(ctx context.Context, voucherID string) (int64, error)
	CountUserVoucherRedemptions(ctx context.Context, voucherID, userID string) (int64, error)
	CreateVoucherRedemption(ctx context.Context, r *domain.VoucherRedemption) error
}

func (s *Store) ListVouchers(ctx context.Context, filter service.VoucherFilter, page, perPage int) ([]domain.Voucher, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	query := s.db.WithContext(ctx).Model(&domain.Voucher{})
	if filter.Active != nil {
		query = query.Where("active = ?", *filter.Active)
	}
	if filter.Code != "" {
		query = query.Where("code ILIKE ?", "%"+filter.Code+"%")
	}
	if filter.DateFrom != nil {
		query = query.Where("created_at >= ?", filter.DateFrom)
	}
	if filter.DateTo != nil {
		query = query.Where("created_at <= ?", filter.DateTo)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count vouchers: %w", err)
	}
	var vouchers []domain.Voucher
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC").Offset(offset).Limit(perPage).Find(&vouchers).Error; err != nil {
		return nil, 0, fmt.Errorf("list vouchers: %w", err)
	}
	return vouchers, total, nil
}

func (s *Store) GetVoucherByID(ctx context.Context, id string) (*domain.Voucher, error) {
	var v domain.Voucher
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&v).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &v, err
}

func (s *Store) GetVoucherByCode(ctx context.Context, code string) (*domain.Voucher, error) {
	var v domain.Voucher
	err := s.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("code = ?", code).First(&v).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &v, err
}

func (s *Store) CreateVoucher(ctx context.Context, v *domain.Voucher) error {
	v.ID = uuid.NewString()
	v.CreatedAt = s.nowUTC()
	v.UpdatedAt = v.CreatedAt
	return s.db.WithContext(ctx).Create(v).Error
}

func (s *Store) UpdateVoucher(ctx context.Context, v *domain.Voucher) error {
	v.UpdatedAt = s.nowUTC()
	return s.db.WithContext(ctx).Where("id = ?", v.ID).Save(v).Error
}

func (s *Store) DeleteVoucher(ctx context.Context, id string, now time.Time) error {
	return s.db.WithContext(ctx).Delete(&domain.Voucher{}, "id = ?", id).Error
}

func (s *Store) CountVoucherRedemptions(ctx context.Context, voucherID string) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&domain.VoucherRedemption{}).Where("voucher_id = ?", voucherID).Count(&count).Error
	return count, err
}

func (s *Store) CountUserVoucherRedemptions(ctx context.Context, voucherID, userID string) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&domain.VoucherRedemption{}).Where("voucher_id = ? AND user_id = ?", voucherID, userID).Count(&count).Error
	return count, err
}

func (s *Store) CreateVoucherRedemption(ctx context.Context, r *domain.VoucherRedemption) error {
	r.ID = uuid.NewString()
	r.CreatedAt = s.nowUTC()
	return s.db.WithContext(ctx).Create(r).Error
}
