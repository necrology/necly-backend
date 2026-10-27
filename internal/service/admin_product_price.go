package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
)

// ProductPriceInput is the admin-facing payload for creating or updating a
// price. Pointer fields make PATCH semantics explicit: nil means "leave the
// stored value unchanged", while a pointer to zero is a real update.
type ProductPriceInput struct {
	DurationID    *string
	Currency      *string
	BaseAmount    *int64
	AdminFee      *int64
	MemberSlots   *int
	AdminIncluded *bool
	// Amount is an explicit per-slot override. When it is nil the service
	// derives Amount from the breakdown so the stored checkout price always
	// matches what the admin preview showed.
	Amount *int64
	Active *bool
}

// PriceRoundIncrement keeps prices readable and gives a small buffer for
// payment fees and exchange-rate differences. The plan documents rounding up
// to the nearest Rp1.000.
const PriceRoundIncrement int64 = 1000

// CalculatePricePerSlot derives the per-member price from the provider cost,
// admin fee, member count and whether the admin takes a share.
//
//	total   = baseAmount + adminFee
//	divisor = memberSlots + (adminIncluded ? 1 : 0)
//	price   = ceil(total / divisor / 1000) * 1000
func CalculatePricePerSlot(baseAmount, adminFee int64, memberSlots int, adminIncluded bool) (price, total int64, divisor int) {
	if baseAmount < 0 {
		baseAmount = 0
	}
	if adminFee < 0 {
		adminFee = 0
	}
	if memberSlots < 0 {
		memberSlots = 0
	}
	total = baseAmount + adminFee
	divisor = memberSlots
	if adminIncluded {
		divisor++
	}
	if total <= 0 || divisor <= 0 {
		return 0, total, divisor
	}
	step := int64(divisor) * PriceRoundIncrement
	price = ((total + step - 1) / step) * PriceRoundIncrement
	return price, total, divisor
}

func (s *AdminProductService) ListProductPrices(ctx context.Context, productID string, includeInactive bool) ([]domain.ProductPrice, error) {
	if _, err := uuid.Parse(productID); err != nil {
		return nil, ErrNotFound
	}
	return s.repo.ListProductPrices(ctx, productID, !includeInactive)
}

func (s *AdminProductService) ListProductDurations(ctx context.Context) ([]domain.ProductDuration, error) {
	return s.repo.ListProductDurations(ctx)
}

func (s *AdminProductService) CreateProductPriceWithBreakdown(ctx context.Context, productID string, input ProductPriceInput) (*domain.ProductPrice, error) {
	if _, err := uuid.Parse(productID); err != nil {
		return nil, ErrNotFound
	}
	if input.DurationID == nil || strings.TrimSpace(*input.DurationID) == "" {
		return nil, ErrInvalidInput
	}
	durationID := strings.TrimSpace(*input.DurationID)
	if _, err := uuid.Parse(durationID); err != nil {
		return nil, ErrInvalidInput
	}

	now := time.Now().UTC()
	price := &domain.ProductPrice{
		ID:          uuid.NewString(),
		ProductID:   productID,
		DurationID:  durationID,
		Currency:    "IDR",
		MemberSlots: 5,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if input.Currency != nil && strings.TrimSpace(*input.Currency) != "" {
		price.Currency = strings.ToUpper(strings.TrimSpace(*input.Currency))
	}
	if input.Active != nil {
		price.Active = *input.Active
	}
	changed, err := applyPriceBreakdown(price, input)
	if err != nil {
		return nil, err
	}
	// Creating a price without any amount or usable breakdown would store an
	// unusable zero row; reject it so "produk tanpa harga" is intentional.
	if !changed && input.Amount == nil {
		return nil, ErrInvalidInput
	}
	if price.Amount < 0 {
		return nil, ErrInvalidInput
	}

	if err := s.repo.CreateProductPrice(ctx, price); err != nil {
		return nil, fmt.Errorf("create product price: %w", err)
	}
	return price, nil
}

func (s *AdminProductService) UpdateProductPriceWithBreakdown(ctx context.Context, productID, priceID string, input ProductPriceInput) (*domain.ProductPrice, error) {
	if _, err := uuid.Parse(productID); err != nil {
		return nil, ErrNotFound
	}
	if _, err := uuid.Parse(priceID); err != nil {
		return nil, ErrNotFound
	}
	price, err := s.repo.GetProductPriceByID(ctx, priceID)
	if err != nil {
		return nil, err
	}
	if price.ProductID != productID {
		return nil, ErrNotFound
	}

	if input.DurationID != nil {
		durationID := strings.TrimSpace(*input.DurationID)
		if _, err := uuid.Parse(durationID); err != nil {
			return nil, ErrInvalidInput
		}
		price.DurationID = durationID
	}
	if input.Currency != nil && strings.TrimSpace(*input.Currency) != "" {
		price.Currency = strings.ToUpper(strings.TrimSpace(*input.Currency))
	}
	if input.Active != nil {
		price.Active = *input.Active
	}
	if _, err := applyPriceBreakdown(price, input); err != nil {
		return nil, err
	}
	if price.Amount < 0 {
		return nil, ErrInvalidInput
	}

	price.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateProductPrice(ctx, price); err != nil {
		return nil, fmt.Errorf("update product price: %w", err)
	}
	return price, nil
}

func (s *AdminProductService) DeleteProductPriceForProduct(ctx context.Context, productID, priceID string) error {
	price, err := s.repo.GetProductPriceByID(ctx, priceID)
	if err != nil {
		return err
	}
	if price.ProductID != productID {
		return ErrNotFound
	}
	return s.repo.DeleteProductPrice(ctx, priceID)
}

// applyPriceBreakdown merges the supplied breakdown values and recomputes
// Amount. It returns true when at least one breakdown field was supplied.
func applyPriceBreakdown(price *domain.ProductPrice, input ProductPriceInput) (bool, error) {
	changed := false
	if input.BaseAmount != nil {
		if *input.BaseAmount < 0 {
			return false, ErrInvalidInput
		}
		price.BaseAmount = *input.BaseAmount
		changed = true
	}
	if input.AdminFee != nil {
		if *input.AdminFee < 0 {
			return false, ErrInvalidInput
		}
		price.AdminFee = *input.AdminFee
		changed = true
	}
	if input.MemberSlots != nil {
		if *input.MemberSlots <= 0 {
			return false, ErrInvalidInput
		}
		price.MemberSlots = *input.MemberSlots
		changed = true
	}
	if input.AdminIncluded != nil {
		price.AdminIncluded = *input.AdminIncluded
		changed = true
	}
	if price.MemberSlots <= 0 {
		price.MemberSlots = 5
	}

	if input.Amount != nil {
		if *input.Amount < 0 {
			return false, ErrInvalidInput
		}
		price.Amount = *input.Amount
		return true, nil
	}
	if changed {
		derived, _, _ := CalculatePricePerSlot(price.BaseAmount, price.AdminFee, price.MemberSlots, price.AdminIncluded)
		price.Amount = derived
	}
	return changed, nil
}
