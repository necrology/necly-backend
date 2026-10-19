package main

import (
	"testing"

	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
)

func priceWith(available int) domain.ProductPriceWithStock {
	return domain.ProductPriceWithStock{AvailableSlots: available}
}

// TestRequiredSharingLimitCoversEveryPackage guards the review finding that
// cmd/seed-inventory sized the account for one package while provisioning one
// slot per package. A product with N packages needs perPrice*N seats.
func TestRequiredSharingLimitCoversEveryPackage(t *testing.T) {
	cases := []struct {
		name        string
		perPrice    int
		prices      []domain.ProductPriceWithStock
		provisioned int64
		want        int
		wantOK      bool
	}{
		{
			name:     "fresh multi-package covers every package",
			perPrice: 3,
			prices:   []domain.ProductPriceWithStock{priceWith(0), priceWith(0)},
			want:     6,
			wantOK:   true,
		},
		{
			name:        "partial provisioning adds only the missing rows",
			perPrice:    3,
			prices:      []domain.ProductPriceWithStock{priceWith(3), priceWith(0)},
			provisioned: 3,
			want:        6,
			wantOK:      true,
		},
		{
			name:        "used slots above the target keep the larger limit",
			perPrice:    3,
			prices:      []domain.ProductPriceWithStock{priceWith(5), priceWith(0)},
			provisioned: 5,
			want:        8,
			wantOK:      true,
		},
		{
			name:     "single package keeps the documented target",
			perPrice: 10,
			prices:   []domain.ProductPriceWithStock{priceWith(0)},
			want:     10,
			wantOK:   true,
		},
		{
			name:     "requirement above the cap fails closed",
			perPrice: 100,
			prices:   make([]domain.ProductPriceWithStock, 11),
			want:     service.MaxSharingLimit,
			wantOK:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := requiredSharingLimit(tc.perPrice, tc.prices, tc.provisioned)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("requiredSharingLimit = %d, %t; want %d, %t", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
