package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/necly/services/internal/domain"
)

type CartRepository interface {
	GetOrCreateCart(ctx context.Context, userID string) (*domain.Cart, error)
	GetCartWithItems(ctx context.Context, userID string) (*domain.Cart, error)
	AddItem(ctx context.Context, cartID, productPriceID string, quantity int, now time.Time) (*domain.CartItem, error)
	UpdateItem(ctx context.Context, cartID, itemID string, quantity int, now time.Time) error
	RemoveItem(ctx context.Context, cartID, itemID string) error
	ClearCart(ctx context.Context, cartID string) error
}

type CartService struct {
	repo CartRepository
}

func NewCartService(repo CartRepository) *CartService {
	return &CartService{repo: repo}
}

func (s *CartService) GetCart(ctx context.Context, userID string) (*domain.Cart, error) {
	return s.repo.GetCartWithItems(ctx, userID)
}

func (s *CartService) AddItem(ctx context.Context, userID, productPriceID string, quantity int) (*domain.CartItem, error) {
	if quantity <= 0 || quantity > 100 {
		return nil, ErrInvalidInput
	}
	cart, err := s.repo.GetOrCreateCart(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return s.repo.AddItem(ctx, cart.ID, productPriceID, quantity, now)
}

func (s *CartService) UpdateItem(ctx context.Context, userID, itemID string, quantity int) error {
	if quantity <= 0 || quantity > 100 {
		return ErrInvalidInput
	}
	cart, err := s.repo.GetOrCreateCart(ctx, userID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.repo.UpdateItem(ctx, cart.ID, itemID, quantity, now)
}

func (s *CartService) RemoveItem(ctx context.Context, userID, itemID string) error {
	cart, err := s.repo.GetOrCreateCart(ctx, userID)
	if err != nil {
		return err
	}
	return s.repo.RemoveItem(ctx, cart.ID, itemID)
}

func (s *CartService) ClearCart(ctx context.Context, userID string) error {
	cart, err := s.repo.GetOrCreateCart(ctx, userID)
	if err != nil {
		return err
	}
	return s.repo.ClearCart(ctx, cart.ID)
}

func (s *CartService) GetCartWithValidation(ctx context.Context, userID string) (*domain.Cart, error) {
	cart, err := s.repo.GetCartWithItems(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return &domain.Cart{UserID: userID, Items: []domain.CartItem{}}, nil
		}
		return nil, err
	}
	for i := range cart.Items {
		if cart.Items[i].ProductPriceID == "" {
			continue
		}
		if cart.Items[i].ProductPrice != nil && !cart.Items[i].ProductPrice.Active {
			return nil, fmt.Errorf("Harga paket langganan %s sudah tidak tersedia", cart.Items[i].ProductPriceID)
		}
	}
	return cart, nil
}
