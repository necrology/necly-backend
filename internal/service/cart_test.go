package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockCartRepository struct {
	mock.Mock
}

func (m *mockCartRepository) GetOrCreateCart(ctx context.Context, userID string) (*domain.Cart, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Cart), args.Error(1)
}

func (m *mockCartRepository) GetCartWithItems(ctx context.Context, userID string) (*domain.Cart, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Cart), args.Error(1)
}

func (m *mockCartRepository) AddItem(ctx context.Context, cartID, productPriceID string, quantity int, now time.Time) (*domain.CartItem, error) {
	args := m.Called(ctx, cartID, productPriceID, quantity, now)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.CartItem), args.Error(1)
}

func (m *mockCartRepository) UpdateItem(ctx context.Context, cartID, itemID string, quantity int, now time.Time) error {
	args := m.Called(ctx, cartID, itemID, quantity, now)
	return args.Error(0)
}

func (m *mockCartRepository) RemoveItem(ctx context.Context, cartID, itemID string) error {
	args := m.Called(ctx, cartID, itemID)
	return args.Error(0)
}

func (m *mockCartRepository) ClearCart(ctx context.Context, cartID string) error {
	args := m.Called(ctx, cartID)
	return args.Error(0)
}

func TestCartService_GetCart(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCartRepository)
	svc := NewCartService(repo)

	userID := uuid.NewString()
	cart := &domain.Cart{ID: uuid.NewString(), UserID: userID, Items: []domain.CartItem{}}

	repo.On("GetCartWithItems", ctx, userID).Return(cart, nil)

	result, err := svc.GetCart(ctx, userID)
	assert.NoError(t, err)
	assert.Equal(t, userID, result.UserID)
	repo.AssertExpectations(t)
}

func TestCartService_AddItem(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCartRepository)
	svc := NewCartService(repo)

	userID := uuid.NewString()
	cart := &domain.Cart{ID: uuid.NewString(), UserID: userID}
	productPriceID := uuid.NewString()
	item := &domain.CartItem{ID: uuid.NewString(), CartID: cart.ID, ProductPriceID: productPriceID, Quantity: 2}

	repo.On("GetOrCreateCart", ctx, userID).Return(cart, nil)
	repo.On("AddItem", ctx, cart.ID, productPriceID, 2, mock.AnythingOfType("time.Time")).Return(item, nil)

	result, err := svc.AddItem(ctx, userID, productPriceID, 2)
	assert.NoError(t, err)
	assert.Equal(t, 2, result.Quantity)
	assert.Equal(t, productPriceID, result.ProductPriceID)
	repo.AssertExpectations(t)
}

func TestCartService_AddItem_InvalidQuantity(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCartRepository)
	svc := NewCartService(repo)

	userID := uuid.NewString()
	productPriceID := uuid.NewString()

	_, err := svc.AddItem(ctx, userID, productPriceID, 0)
	assert.Error(t, err)
	assert.Equal(t, ErrInvalidInput, err)

	_, err = svc.AddItem(ctx, userID, productPriceID, 101)
	assert.Error(t, err)
	assert.Equal(t, ErrInvalidInput, err)
}

func TestCartService_UpdateItem(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCartRepository)
	svc := NewCartService(repo)

	userID := uuid.NewString()
	cart := &domain.Cart{ID: uuid.NewString(), UserID: userID}
	itemID := uuid.NewString()

	repo.On("GetOrCreateCart", ctx, userID).Return(cart, nil)
	repo.On("UpdateItem", ctx, cart.ID, itemID, 3, mock.AnythingOfType("time.Time")).Return(nil)

	err := svc.UpdateItem(ctx, userID, itemID, 3)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestCartService_UpdateItem_InvalidQuantity(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCartRepository)
	svc := NewCartService(repo)

	userID := uuid.NewString()
	itemID := uuid.NewString()

	err := svc.UpdateItem(ctx, userID, itemID, 0)
	assert.Error(t, err)
	assert.Equal(t, ErrInvalidInput, err)

	err = svc.UpdateItem(ctx, userID, itemID, 101)
	assert.Error(t, err)
	assert.Equal(t, ErrInvalidInput, err)
}

func TestCartService_RemoveItem(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCartRepository)
	svc := NewCartService(repo)

	userID := uuid.NewString()
	cart := &domain.Cart{ID: uuid.NewString(), UserID: userID}
	itemID := uuid.NewString()

	repo.On("GetOrCreateCart", ctx, userID).Return(cart, nil)
	repo.On("RemoveItem", ctx, cart.ID, itemID).Return(nil)

	err := svc.RemoveItem(ctx, userID, itemID)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestCartService_ClearCart(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCartRepository)
	svc := NewCartService(repo)

	userID := uuid.NewString()
	cart := &domain.Cart{ID: uuid.NewString(), UserID: userID}

	repo.On("GetOrCreateCart", ctx, userID).Return(cart, nil)
	repo.On("ClearCart", ctx, cart.ID).Return(nil)

	err := svc.ClearCart(ctx, userID)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestCartService_GetCartWithValidation(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCartRepository)
	svc := NewCartService(repo)

	userID := uuid.NewString()
	cart := &domain.Cart{
		ID:     uuid.NewString(),
		UserID: userID,
		Items: []domain.CartItem{
			{ID: uuid.NewString(), ProductPriceID: uuid.NewString(), Quantity: 1},
		},
	}

	repo.On("GetCartWithItems", ctx, userID).Return(cart, nil)

	result, err := svc.GetCartWithValidation(ctx, userID)
	assert.NoError(t, err)
	assert.Equal(t, userID, result.UserID)
	repo.AssertExpectations(t)
}

func TestCartService_GetCartWithValidation_EmptyCart(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCartRepository)
	svc := NewCartService(repo)

	userID := uuid.NewString()

	repo.On("GetCartWithItems", ctx, userID).Return(nil, ErrNotFound)

	result, err := svc.GetCartWithValidation(ctx, userID)
	assert.NoError(t, err)
	assert.Equal(t, userID, result.UserID)
	assert.Len(t, result.Items, 0)
	repo.AssertExpectations(t)
}

func TestCartService_GetCartWithValidation_InactivePrice(t *testing.T) {
	ctx := context.Background()
	repo := new(mockCartRepository)
	svc := NewCartService(repo)

	userID := uuid.NewString()
	priceID := uuid.NewString()
	cart := &domain.Cart{
		ID:     uuid.NewString(),
		UserID: userID,
		Items: []domain.CartItem{
			{
				ID:             uuid.NewString(),
				ProductPriceID: priceID,
				Quantity:       1,
				ProductPrice:   &domain.ProductPrice{ID: priceID, Active: false},
			},
		},
	}

	repo.On("GetCartWithItems", ctx, userID).Return(cart, nil)

	result, err := svc.GetCartWithValidation(ctx, userID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sudah tidak tersedia")
	assert.Nil(t, result)
	repo.AssertExpectations(t)
}
