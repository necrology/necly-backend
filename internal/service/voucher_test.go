package service

import (
	"errors"
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
)

func TestVoucherPercentageHonorsMaximumDiscount(t *testing.T) {
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	voucher := domain.Voucher{Active: true, Type: domain.VoucherPercentage, Value: 25, MaxDiscount: 30_000, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)}
	discount, err := CalculateVoucherDiscount(voucher, 200_000, 0, 0, now)
	if err != nil {
		t.Fatalf("CalculateVoucherDiscount() error = %v", err)
	}
	if discount != 30_000 {
		t.Fatalf("discount = %d, want 30000", discount)
	}
}

func TestVoucherFixedDiscountNeverMakesNegativeTotal(t *testing.T) {
	now := time.Now().UTC()
	voucher := domain.Voucher{Active: true, Type: domain.VoucherFixed, Value: 100_000, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)}
	discount, err := CalculateVoucherDiscount(voucher, 50_000, 0, 0, now)
	if err != nil || discount != 50_000 {
		t.Fatalf("discount, err = %d, %v; want 50000, nil", discount, err)
	}
}

func TestVoucherRejectsEligibilityEdgeCases(t *testing.T) {
	now := time.Now().UTC()
	base := domain.Voucher{Active: true, Type: domain.VoucherFixed, Value: 10_000, MinOrder: 50_000, UsageLimit: 2, PerUserLimit: 1, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)}
	tests := []struct {
		name      string
		mutate    func(*domain.Voucher)
		subtotal  int64
		used      int
		userUsed  int
		wantError error
	}{
		{name: "inactive", mutate: func(v *domain.Voucher) { v.Active = false }, subtotal: 60_000, wantError: ErrVoucherInactive},
		{name: "not started", mutate: func(v *domain.Voucher) { v.StartsAt = now.Add(time.Minute) }, subtotal: 60_000, wantError: ErrVoucherNotStarted},
		{name: "expired", mutate: func(v *domain.Voucher) { v.EndsAt = now.Add(-time.Minute) }, subtotal: 60_000, wantError: ErrVoucherExpired},
		{name: "minimum", mutate: func(v *domain.Voucher) {}, subtotal: 49_999, wantError: ErrVoucherMinimum},
		{name: "global limit", mutate: func(v *domain.Voucher) {}, subtotal: 60_000, used: 2, wantError: ErrVoucherExhausted},
		{name: "user limit", mutate: func(v *domain.Voucher) {}, subtotal: 60_000, userUsed: 1, wantError: ErrVoucherUserLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			voucher := base
			tt.mutate(&voucher)
			_, err := CalculateVoucherDiscount(voucher, tt.subtotal, tt.used, tt.userUsed, now)
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("error = %v, want %v", err, tt.wantError)
			}
		})
	}
}
