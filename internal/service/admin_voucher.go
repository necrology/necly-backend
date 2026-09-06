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

type AdminVoucherService struct {
	repo AdminVoucherRepository
}

func NewAdminVoucherService(repo AdminVoucherRepository) *AdminVoucherService {
	return &AdminVoucherService{repo: repo}
}

func (s *AdminVoucherService) ListVouchers(ctx context.Context, filter VoucherFilter, page, perPage int) ([]domain.Voucher, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	return s.repo.ListVouchers(ctx, filter, page, perPage)
}

func (s *AdminVoucherService) GetVoucher(ctx context.Context, id string) (*domain.Voucher, error) {
	return s.repo.GetVoucherByID(ctx, id)
}

func (s *AdminVoucherService) GetVoucherByCode(ctx context.Context, code string) (*domain.Voucher, error) {
	return s.repo.GetVoucherByCode(ctx, code)
}

type CreateVoucherInput struct {
	Code         string             `json:"code"`
	Type         domain.VoucherType `json:"type"`
	Value        int64              `json:"value"`
	MinOrder     int64              `json:"min_order"`
	MaxDiscount  int64              `json:"max_discount"`
	UsageLimit   int                `json:"usage_limit"`
	PerUserLimit int                `json:"per_user_limit"`
	StartsAt     time.Time          `json:"starts_at"`
	EndsAt       time.Time          `json:"ends_at"`
	Active       bool               `json:"active"`
}

func (s *AdminVoucherService) CreateVoucher(ctx context.Context, input CreateVoucherInput) (*domain.Voucher, error) {
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	if input.Code == "" || input.Type == "" || (input.Type != domain.VoucherFixed && input.Type != domain.VoucherPercentage) || input.Value <= 0 {
		return nil, ErrInvalidInput
	}
	if input.Type == domain.VoucherPercentage && (input.Value < 1 || input.Value > 100) {
		return nil, ErrInvalidInput
	}
	if input.StartsAt.IsZero() || input.EndsAt.IsZero() || !input.EndsAt.After(input.StartsAt) {
		return nil, ErrInvalidInput
	}

	existing, err := s.repo.GetVoucherByCode(ctx, input.Code)
	if err == nil && existing != nil {
		return nil, ErrConflict
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("check existing voucher: %w", err)
	}

	now := time.Now().UTC()
	voucher := &domain.Voucher{
		ID:           uuid.NewString(),
		Code:         input.Code,
		Type:         input.Type,
		Value:        input.Value,
		MinOrder:     input.MinOrder,
		MaxDiscount:  input.MaxDiscount,
		UsageLimit:   input.UsageLimit,
		PerUserLimit: input.PerUserLimit,
		StartsAt:     input.StartsAt.UTC(),
		EndsAt:       input.EndsAt.UTC(),
		Active:       input.Active,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.repo.CreateVoucher(ctx, voucher); err != nil {
		return nil, fmt.Errorf("create voucher: %w", err)
	}
	return voucher, nil
}

type UpdateVoucherInput struct {
	Code         *string             `json:"code"`
	Type         *domain.VoucherType `json:"type"`
	Value        *int64              `json:"value"`
	MinOrder     *int64              `json:"min_order"`
	MaxDiscount  *int64              `json:"max_discount"`
	UsageLimit   *int                `json:"usage_limit"`
	PerUserLimit *int                `json:"per_user_limit"`
	StartsAt     *time.Time          `json:"starts_at"`
	EndsAt       *time.Time          `json:"ends_at"`
	Active       *bool               `json:"active"`
}

func (s *AdminVoucherService) UpdateVoucher(ctx context.Context, id string, input UpdateVoucherInput) (*domain.Voucher, error) {
	voucher, err := s.repo.GetVoucherByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if input.Code != nil {
		code := strings.ToUpper(strings.TrimSpace(*input.Code))
		if code != voucher.Code {
			existing, err := s.repo.GetVoucherByCode(ctx, code)
			if err == nil && existing != nil && existing.ID != id {
				return nil, ErrConflict
			}
			if !errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("check existing voucher: %w", err)
			}
			voucher.Code = code
		}
	}
	if input.Type != nil {
		if *input.Type != domain.VoucherFixed && *input.Type != domain.VoucherPercentage {
			return nil, ErrInvalidInput
		}
		voucher.Type = *input.Type
	}
	if input.Value != nil {
		if *input.Value <= 0 {
			return nil, ErrInvalidInput
		}
		if voucher.Type == domain.VoucherPercentage && (*input.Value < 1 || *input.Value > 100) {
			return nil, ErrInvalidInput
		}
		voucher.Value = *input.Value
	}
	if input.MinOrder != nil {
		voucher.MinOrder = *input.MinOrder
	}
	if input.MaxDiscount != nil {
		voucher.MaxDiscount = *input.MaxDiscount
	}
	if input.UsageLimit != nil {
		voucher.UsageLimit = *input.UsageLimit
	}
	if input.PerUserLimit != nil {
		voucher.PerUserLimit = *input.PerUserLimit
	}
	if input.StartsAt != nil {
		voucher.StartsAt = input.StartsAt.UTC()
	}
	if input.EndsAt != nil {
		voucher.EndsAt = input.EndsAt.UTC()
	}
	if input.Active != nil {
		voucher.Active = *input.Active
	}

	if voucher.StartsAt.IsZero() || voucher.EndsAt.IsZero() || !voucher.EndsAt.After(voucher.StartsAt) {
		return nil, ErrInvalidInput
	}

	voucher.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateVoucher(ctx, voucher); err != nil {
		return nil, fmt.Errorf("update voucher: %w", err)
	}
	return voucher, nil
}

func (s *AdminVoucherService) DeleteVoucher(ctx context.Context, id string) error {
	now := time.Now().UTC()
	return s.repo.DeleteVoucher(ctx, id, now)
}
