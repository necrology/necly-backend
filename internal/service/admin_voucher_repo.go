package service

import (
	"context"
	"time"

	"github.com/necly/services/internal/domain"
)

type AdminVoucherRepository interface {
	ListVouchers(ctx context.Context, filter VoucherFilter, page, perPage int) ([]domain.Voucher, int64, error)
	GetVoucherByID(ctx context.Context, id string) (*domain.Voucher, error)
	GetVoucherByCode(ctx context.Context, code string) (*domain.Voucher, error)
	CreateVoucher(ctx context.Context, v *domain.Voucher) error
	UpdateVoucher(ctx context.Context, v *domain.Voucher) error
	DeleteVoucher(ctx context.Context, id string, now time.Time) error
}

type VoucherFilter struct {
	Active   *bool
	Code     string
	DateFrom *time.Time
	DateTo   *time.Time
}
