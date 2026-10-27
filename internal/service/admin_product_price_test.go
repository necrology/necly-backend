package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCalculatePricePerSlot_AdminIncluded(t *testing.T) {
	price, total, divisor := CalculatePricePerSlot(300_000, 30_000, 5, true)
	assert.Equal(t, int64(330_000), total)
	assert.Equal(t, 6, divisor)
	assert.Equal(t, int64(55_000), price)
}

func TestCalculatePricePerSlot_AdminNotIncluded(t *testing.T) {
	price, total, divisor := CalculatePricePerSlot(300_000, 30_000, 5, false)
	assert.Equal(t, int64(330_000), total)
	assert.Equal(t, 5, divisor)
	assert.Equal(t, int64(66_000), price)
}

func TestCalculatePricePerSlot_RoundsUpToThousand(t *testing.T) {
	// 100.001 / 5 = 20.000,2 -> 21.000, bukan 20.000.
	price, _, _ := CalculatePricePerSlot(100_001, 0, 5, false)
	assert.Equal(t, int64(21_000), price)

	// Sudah kelipatan seribu tidak ikut naik.
	price, _, _ = CalculatePricePerSlot(100_000, 0, 5, false)
	assert.Equal(t, int64(20_000), price)
}

func TestCalculatePricePerSlot_ZeroInputs(t *testing.T) {
	price, total, divisor := CalculatePricePerSlot(0, 0, 5, true)
	assert.Zero(t, total)
	assert.Equal(t, 6, divisor)
	assert.Zero(t, price)
}

func TestAdminProductService_CreateProductPriceWithBreakdown(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	productID := uuid.NewString()
	durationID := uuid.NewString()
	base := int64(300_000)
	fee := int64(30_000)
	slots := 5
	included := true

	repo.On("CreateProductPrice", ctx, mock.AnythingOfType("*domain.ProductPrice")).Return(nil).Run(func(args mock.Arguments) {
		price := args.Get(1).(*domain.ProductPrice)
		assert.Equal(t, productID, price.ProductID)
		assert.Equal(t, durationID, price.DurationID)
		assert.Equal(t, base, price.BaseAmount)
		assert.Equal(t, fee, price.AdminFee)
		assert.Equal(t, slots, price.MemberSlots)
		assert.True(t, price.AdminIncluded)
		assert.Equal(t, int64(55_000), price.Amount)
		assert.Equal(t, "IDR", price.Currency)
		assert.True(t, price.Active)
	})

	price, err := svc.CreateProductPriceWithBreakdown(ctx, productID, ProductPriceInput{
		DurationID:    &durationID,
		BaseAmount:    &base,
		AdminFee:      &fee,
		MemberSlots:   &slots,
		AdminIncluded: &included,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(55_000), price.Amount)
	repo.AssertExpectations(t)
}

func TestAdminProductService_CreateProductPriceWithBreakdown_RequiresAmountOrBreakdown(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	durationID := uuid.NewString()
	_, err := svc.CreateProductPriceWithBreakdown(ctx, uuid.NewString(), ProductPriceInput{DurationID: &durationID})
	assert.ErrorIs(t, err, ErrInvalidInput)
	repo.AssertNotCalled(t, "CreateProductPrice", mock.Anything, mock.Anything)
}

func TestAdminProductService_UpdateProductPriceWithBreakdown_RecomputesAmount(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	productID := uuid.NewString()
	priceID := uuid.NewString()
	durationID := uuid.NewString()
	stored := &domain.ProductPrice{
		ID: priceID, ProductID: productID, DurationID: durationID,
		Amount: 55_000, BaseAmount: 300_000, AdminFee: 30_000,
		MemberSlots: 5, AdminIncluded: true, Currency: "IDR", Active: true,
	}
	repo.On("GetProductPriceByID", ctx, priceID).Return(stored, nil)
	repo.On("UpdateProductPrice", ctx, mock.AnythingOfType("*domain.ProductPrice")).Return(nil).Run(func(args mock.Arguments) {
		price := args.Get(1).(*domain.ProductPrice)
		assert.Equal(t, int64(66_000), price.Amount)
		assert.False(t, price.AdminIncluded)
	})

	included := false
	price, err := svc.UpdateProductPriceWithBreakdown(ctx, productID, priceID, ProductPriceInput{AdminIncluded: &included})
	require.NoError(t, err)
	assert.Equal(t, int64(66_000), price.Amount)
	repo.AssertExpectations(t)
}

func TestAdminProductService_UpdateProductPriceWithBreakdown_WrongProduct(t *testing.T) {
	ctx := context.Background()
	repo := new(mockAdminProductRepository)
	svc := NewAdminProductService(repo)

	productID := uuid.NewString()
	priceID := uuid.NewString()
	repo.On("GetProductPriceByID", ctx, priceID).Return(&domain.ProductPrice{
		ID: priceID, ProductID: uuid.NewString(), DurationID: uuid.NewString(), Amount: 10_000,
	}, nil)

	_, err := svc.UpdateProductPriceWithBreakdown(ctx, productID, priceID, ProductPriceInput{})
	assert.ErrorIs(t, err, ErrNotFound)
	repo.AssertNotCalled(t, "UpdateProductPrice", mock.Anything, mock.Anything)
}
