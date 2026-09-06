package service

import (
	"errors"
	"time"

	"github.com/necly/services/internal/domain"
)

var (
	ErrVoucherInactive   = errors.New("Voucher tidak aktif")
	ErrVoucherNotStarted = errors.New("Periode voucher belum dimulai")
	ErrVoucherExpired    = errors.New("Masa berlaku voucher telah berakhir")
	ErrVoucherMinimum    = errors.New("Total pesanan belum memenuhi syarat minimum voucher")
	ErrVoucherExhausted  = errors.New("Kuota penggunaan voucher telah habis")
	ErrVoucherUserLimit  = errors.New("Batas penggunaan voucher per pengguna telah tercapai")
	ErrVoucherInvalid    = errors.New("Konfigurasi voucher tidak valid")
)

func CalculateVoucherDiscount(v domain.Voucher, subtotal int64, used, userUsed int, now time.Time) (int64, error) {
	if !v.Active {
		return 0, ErrVoucherInactive
	}
	if now.Before(v.StartsAt) {
		return 0, ErrVoucherNotStarted
	}
	if !v.EndsAt.IsZero() && now.After(v.EndsAt) {
		return 0, ErrVoucherExpired
	}
	if subtotal < v.MinOrder {
		return 0, ErrVoucherMinimum
	}
	if v.UsageLimit > 0 && used >= v.UsageLimit {
		return 0, ErrVoucherExhausted
	}
	if v.PerUserLimit > 0 && userUsed >= v.PerUserLimit {
		return 0, ErrVoucherUserLimit
	}
	var discount int64
	switch v.Type {
	case domain.VoucherFixed:
		discount = v.Value
	case domain.VoucherPercentage:
		if v.Value < 0 || v.Value > 100 {
			return 0, ErrVoucherInvalid
		}
		discount = subtotal * v.Value / 100
		if v.MaxDiscount > 0 && discount > v.MaxDiscount {
			discount = v.MaxDiscount
		}
	default:
		return 0, ErrVoucherInvalid
	}
	if discount < 0 {
		return 0, ErrVoucherInvalid
	}
	if discount > subtotal {
		discount = subtotal
	}
	return discount, nil
}
