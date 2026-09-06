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

type CartRepository interface {
	GetOrCreateCart(ctx context.Context, userID string) (*domain.Cart, error)
	GetCartWithItems(ctx context.Context, userID string) (*domain.Cart, error)
	AddItem(ctx context.Context, cartID, productPriceID string, quantity int, now time.Time) (*domain.CartItem, error)
	UpdateItem(ctx context.Context, cartID, itemID string, quantity int, now time.Time) error
	RemoveItem(ctx context.Context, cartID, itemID string) error
	ClearCart(ctx context.Context, cartID string) error
}

func (s *Store) GetOrCreateCart(ctx context.Context, userID string) (*domain.Cart, error) {
	var cart domain.Cart
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).First(&cart).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			cart = domain.Cart{ID: uuid.NewString(), UserID: userID, CreatedAt: s.nowUTC(), UpdatedAt: s.nowUTC()}
			if err := tx.Create(&cart).Error; err != nil {
				return fmt.Errorf("create cart: %w", err)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("find cart: %w", err)
		}
		return nil
	})
	return &cart, err
}

func (s *Store) GetCartWithItems(ctx context.Context, userID string) (*domain.Cart, error) {
	var cart domain.Cart
	err := s.db.WithContext(ctx).Preload("Items.ProductPrice").Preload("Items.ProductPrice.Duration").
		Where("user_id = ?", userID).First(&cart).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &cart, err
}

func (s *Store) AddItem(ctx context.Context, cartID, productPriceID string, quantity int, now time.Time) (*domain.CartItem, error) {
	var item domain.CartItem
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var price domain.ProductPrice
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND active = ?", productPriceID, true).First(&price).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return fmt.Errorf("find price: %w", err)
		}

		var existing domain.CartItem
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("cart_id = ? AND product_price_id = ?", cartID, productPriceID).First(&existing).Error
		if err == nil {
			existing.Quantity += quantity
			if existing.Quantity > 100 {
				existing.Quantity = 100
			}
			existing.UpdatedAt = now
			if err := tx.Save(&existing).Error; err != nil {
				return fmt.Errorf("update cart item: %w", err)
			}
			item = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("check existing item: %w", err)
		}

		item = domain.CartItem{
			ID:             uuid.NewString(),
			CartID:         cartID,
			ProductPriceID: productPriceID,
			Quantity:       quantity,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := tx.Create(&item).Error; err != nil {
			return fmt.Errorf("create cart item: %w", err)
		}
		return nil
	})
	return &item, err
}

func (s *Store) UpdateItem(ctx context.Context, cartID, itemID string, quantity int, now time.Time) error {
	if quantity <= 0 || quantity > 100 {
		return service.ErrInvalidInput
	}
	result := s.db.WithContext(ctx).Model(&domain.CartItem{}).
		Where("id = ? AND cart_id = ?", itemID, cartID).
		Updates(map[string]any{"quantity": quantity, "updated_at": now})
	if result.Error != nil {
		return fmt.Errorf("update cart item: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return service.ErrNotFound
	}
	return nil
}

func (s *Store) RemoveItem(ctx context.Context, cartID, itemID string) error {
	result := s.db.WithContext(ctx).Where("id = ? AND cart_id = ?", itemID, cartID).Delete(&domain.CartItem{})
	if result.Error != nil {
		return fmt.Errorf("remove cart item: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return service.ErrNotFound
	}
	return nil
}

func (s *Store) ClearCart(ctx context.Context, cartID string) error {
	return s.db.WithContext(ctx).Where("cart_id = ?", cartID).Delete(&domain.CartItem{}).Error
}
