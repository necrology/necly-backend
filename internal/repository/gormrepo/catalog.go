package gormrepo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
	"gorm.io/gorm"
)

type CatalogRepository interface {
	ListCategories(ctx context.Context, activeOnly bool, page, perPage int) ([]domain.Category, int64, error)
	GetCategoryByID(ctx context.Context, id string) (*domain.Category, error)
	GetCategoryBySlug(ctx context.Context, slug string) (*domain.Category, error)
	CreateCategory(ctx context.Context, c *domain.Category) error
	UpdateCategory(ctx context.Context, c *domain.Category) error
	DeleteCategory(ctx context.Context, id string, now time.Time) error
	ListCategoriesWithFilter(ctx context.Context, filter service.CategoryFilter, page, perPage int) ([]domain.Category, int64, error)

	ListProducts(ctx context.Context, filter service.ProductFilter, page, perPage int) ([]domain.Product, int64, error)
	GetProductByID(ctx context.Context, id string) (*domain.Product, error)
	GetProductBySlug(ctx context.Context, slug string) (*domain.Product, error)
	CreateProduct(ctx context.Context, p *domain.Product) error
	UpdateProduct(ctx context.Context, p *domain.Product) error
	DeleteProduct(ctx context.Context, id string, now time.Time) error

	ListProductPrices(ctx context.Context, productID string, activeOnly bool) ([]domain.ProductPrice, error)
	GetProductPriceByID(ctx context.Context, id string) (*domain.ProductPrice, error)
	CreateProductPrice(ctx context.Context, p *domain.ProductPrice) error
	UpdateProductPrice(ctx context.Context, p *domain.ProductPrice) error
	DeleteProductPrice(ctx context.Context, id string) error

	// ListProductsWithStock returns products with prices including available slot counts
	ListProductsWithStock(ctx context.Context, filter service.ProductFilter, page, perPage int) ([]domain.ProductWithStock, int64, error)
	GetProductWithStock(ctx context.Context, id string) (*domain.ProductWithStock, error)
	GetProductBySlugWithStock(ctx context.Context, slug string) (*domain.ProductWithStock, error)
}

func (s *Store) ListCategories(ctx context.Context, activeOnly bool, page, perPage int) ([]domain.Category, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	query := s.db.WithContext(ctx).Model(&domain.Category{})
	if activeOnly {
		query = query.Where("active = ?", true)
	}
	query = query.Where("deleted_at IS NULL")
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count categories: %w", err)
	}
	var categories []domain.Category
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC").Offset(offset).Limit(perPage).Find(&categories).Error; err != nil {
		return nil, 0, fmt.Errorf("list categories: %w", err)
	}
	return categories, total, nil
}

func (s *Store) ListCategoriesAdmin(ctx context.Context, filter service.CategoryFilter, page, perPage int) ([]domain.Category, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	query := s.db.WithContext(ctx).Model(&domain.Category{}).Where("deleted_at IS NULL")
	if filter.Active != nil {
		query = query.Where("active = ?", *filter.Active)
	}
	if filter.Search != "" {
		query = query.Where("name ILIKE ? OR description ILIKE ?", "%"+filter.Search+"%", "%"+filter.Search+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count categories: %w", err)
	}
	var categories []domain.Category
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC").Offset(offset).Limit(perPage).Find(&categories).Error; err != nil {
		return nil, 0, fmt.Errorf("list categories: %w", err)
	}
	return categories, total, nil
}

func (s *Store) GetCategoryByID(ctx context.Context, id string) (*domain.Category, error) {
	var cat domain.Category
	err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", id).First(&cat).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &cat, err
}

func (s *Store) GetCategoryBySlug(ctx context.Context, slug string) (*domain.Category, error) {
	var cat domain.Category
	err := s.db.WithContext(ctx).Where("slug = ? AND deleted_at IS NULL", slug).First(&cat).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &cat, err
}

func (s *Store) CreateCategory(ctx context.Context, c *domain.Category) error {
	c.ID = uuid.NewString()
	c.CreatedAt = s.nowUTC()
	c.UpdatedAt = c.CreatedAt
	return s.db.WithContext(ctx).Create(c).Error
}

func (s *Store) UpdateCategory(ctx context.Context, c *domain.Category) error {
	c.UpdatedAt = s.nowUTC()
	return s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", c.ID).Save(c).Error
}

func (s *Store) DeleteCategory(ctx context.Context, id string, now time.Time) error {
	return s.db.WithContext(ctx).Model(&domain.Category{}).
		Where("id = ? AND deleted_at IS NULL", id).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}

func (s *Store) ListCategoriesWithFilter(ctx context.Context, filter service.CategoryFilter, page, perPage int) ([]domain.Category, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	query := s.db.WithContext(ctx).Model(&domain.Category{}).Where("deleted_at IS NULL")
	if filter.Active != nil {
		query = query.Where("active = ?", *filter.Active)
	}
	if filter.Search != "" {
		query = query.Where("name ILIKE ? OR description ILIKE ?", "%"+filter.Search+"%", "%"+filter.Search+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count categories: %w", err)
	}
	var categories []domain.Category
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC").Offset(offset).Limit(perPage).Find(&categories).Error; err != nil {
		return nil, 0, fmt.Errorf("list categories: %w", err)
	}
	return categories, total, nil
}

func (s *Store) ListProducts(ctx context.Context, filter service.ProductFilter, page, perPage int) ([]domain.Product, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	query := s.db.WithContext(ctx).Model(&domain.Product{}).Preload("Category").Preload("Prices")
	query = query.Where("deleted_at IS NULL")
	if filter.ActiveOnly {
		query = query.Where("active = ?", true)
	}
	if filter.CategoryID != "" {
		query = query.Where("category_id = ?", filter.CategoryID)
	}
	if filter.Search != "" {
		like := "%" + strings.ToLower(filter.Search) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(description) LIKE ?", like, like)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count products: %w", err)
	}
	var products []domain.Product
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC").Offset(offset).Limit(perPage).Find(&products).Error; err != nil {
		return nil, 0, fmt.Errorf("list products: %w", err)
	}
	return products, total, nil
}

func (s *Store) GetProductByID(ctx context.Context, id string) (*domain.Product, error) {
	var p domain.Product
	err := s.db.WithContext(ctx).Preload("Category").Preload("Prices").
		Where("id = ? AND deleted_at IS NULL", id).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &p, err
}

func (s *Store) GetProductBySlug(ctx context.Context, slug string) (*domain.Product, error) {
	var p domain.Product
	err := s.db.WithContext(ctx).Preload("Category").Preload("Prices").
		Where("slug = ? AND deleted_at IS NULL", slug).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &p, err
}

func (s *Store) CreateProduct(ctx context.Context, p *domain.Product) error {
	p.ID = uuid.NewString()
	p.CreatedAt = s.nowUTC()
	p.UpdatedAt = p.CreatedAt
	return s.db.WithContext(ctx).Create(p).Error
}

func (s *Store) UpdateProduct(ctx context.Context, p *domain.Product) error {
	p.UpdatedAt = s.nowUTC()
	return s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", p.ID).Save(p).Error
}

func (s *Store) DeleteProduct(ctx context.Context, id string, now time.Time) error {
	return s.db.WithContext(ctx).Model(&domain.Product{}).
		Where("id = ? AND deleted_at IS NULL", id).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}

func (s *Store) ListProductPrices(ctx context.Context, productID string, activeOnly bool) ([]domain.ProductPrice, error) {
	var prices []domain.ProductPrice
	query := s.db.WithContext(ctx).Where("product_id = ?", productID)
	if activeOnly {
		query = query.Where("active = ?", true)
	}
	if err := query.Order("amount ASC").Find(&prices).Error; err != nil {
		return nil, fmt.Errorf("list product prices: %w", err)
	}
	return prices, nil
}

func (s *Store) GetProductPriceByID(ctx context.Context, id string) (*domain.ProductPrice, error) {
	var price domain.ProductPrice
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&price).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &price, err
}

func (s *Store) CreateProductPrice(ctx context.Context, p *domain.ProductPrice) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = s.nowUTC()
	}
	p.UpdatedAt = s.nowUTC()
	return s.db.WithContext(ctx).Create(p).Error
}

func (s *Store) UpdateProductPrice(ctx context.Context, p *domain.ProductPrice) error {
	p.UpdatedAt = s.nowUTC()
	return s.db.WithContext(ctx).Where("id = ?", p.ID).Save(p).Error
}

func (s *Store) DeleteProductPrice(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&domain.ProductPrice{}, "id = ?", id).Error
}

func (s *Store) ListProductsWithStock(ctx context.Context, filter service.ProductFilter, page, perPage int) ([]domain.ProductWithStock, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}

	query := s.db.WithContext(ctx).Model(&domain.Product{}).Preload("Category").Preload("Prices.Duration")
	query = query.Where("deleted_at IS NULL")
	if filter.ActiveOnly {
		query = query.Where("active = ?", true)
	}
	if filter.CategoryID != "" {
		query = query.Where("category_id = ?", filter.CategoryID)
	}
	if filter.Search != "" {
		like := "%" + strings.ToLower(filter.Search) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(description) LIKE ?", like, like)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count products: %w", err)
	}

	var products []domain.Product
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC").Offset(offset).Limit(perPage).Find(&products).Error; err != nil {
		return nil, 0, fmt.Errorf("list products: %w", err)
	}

	// Get product IDs for stock lookup
	productIDs := make([]string, len(products))
	for i, p := range products {
		productIDs[i] = p.ID
	}

	// Get available slots per product_price in a single query
	type slotCount struct {
		ProductPriceID string
		Count          int
	}
	var slotCounts []slotCount
	if len(productIDs) > 0 {
		// Get all product_price IDs for these products
		var priceIDs []string
		s.db.WithContext(ctx).Model(&domain.ProductPrice{}).Where("product_id IN ?", productIDs).Pluck("id", &priceIDs)

		if len(priceIDs) > 0 {
			s.db.WithContext(ctx).Model(&domain.InventorySlot{}).
				Select("product_price_id, COUNT(*) as count").
				Where("product_price_id IN ? AND status = ?", priceIDs, "available").
				Group("product_price_id").
				Scan(&slotCounts)
		}
	}

	// Build slot count map
	slotCountMap := make(map[string]int)
	for _, sc := range slotCounts {
		slotCountMap[sc.ProductPriceID] = sc.Count
	}

	// Build ProductWithStock results
	results := make([]domain.ProductWithStock, len(products))
	for i, p := range products {
		pricesWithStock := make([]domain.ProductPriceWithStock, len(p.Prices))
		for j, price := range p.Prices {
			pricesWithStock[j] = domain.ProductPriceWithStock{
				ProductPrice:   price,
				AvailableSlots: slotCountMap[price.ID],
			}
		}
		results[i] = domain.ProductWithStock{
			Product: p,
			Prices:  pricesWithStock,
		}
	}

	return results, total, nil
}

func (s *Store) GetProductWithStock(ctx context.Context, id string) (*domain.ProductWithStock, error) {
	var p domain.Product
	err := s.db.WithContext(ctx).Preload("Category").Preload("Prices.Duration").
		Where("id = ? AND deleted_at IS NULL", id).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get product: %w", err)
	}

	// Get available slots for this product's prices
	var priceIDs []string
	for _, price := range p.Prices {
		priceIDs = append(priceIDs, price.ID)
	}

	slotCountMap := make(map[string]int)
	if len(priceIDs) > 0 {
		type slotCount struct {
			ProductPriceID string
			Count          int
		}
		var slotCounts []slotCount
		s.db.WithContext(ctx).Model(&domain.InventorySlot{}).
			Select("product_price_id, COUNT(*) as count").
			Where("product_price_id IN ? AND status = ?", priceIDs, "available").
			Group("product_price_id").
			Scan(&slotCounts)
		for _, sc := range slotCounts {
			slotCountMap[sc.ProductPriceID] = sc.Count
		}
	}

	pricesWithStock := make([]domain.ProductPriceWithStock, len(p.Prices))
	for i, price := range p.Prices {
		pricesWithStock[i] = domain.ProductPriceWithStock{
			ProductPrice:   price,
			AvailableSlots: slotCountMap[price.ID],
		}
	}

	return &domain.ProductWithStock{
		Product: p,
		Prices:  pricesWithStock,
	}, nil
}

func (s *Store) GetProductBySlugWithStock(ctx context.Context, slug string) (*domain.ProductWithStock, error) {
	var p domain.Product
	err := s.db.WithContext(ctx).Preload("Category").Preload("Prices.Duration").
		Where("slug = ? AND deleted_at IS NULL", slug).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get product by slug: %w", err)
	}

	// Get available slots for this product's prices
	var priceIDs []string
	for _, price := range p.Prices {
		priceIDs = append(priceIDs, price.ID)
	}

	slotCountMap := make(map[string]int)
	if len(priceIDs) > 0 {
		type slotCount struct {
			ProductPriceID string
			Count          int
		}
		var slotCounts []slotCount
		s.db.WithContext(ctx).Model(&domain.InventorySlot{}).
			Select("product_price_id, COUNT(*) as count").
			Where("product_price_id IN ? AND status = ?", priceIDs, "available").
			Group("product_price_id").
			Scan(&slotCounts)
		for _, sc := range slotCounts {
			slotCountMap[sc.ProductPriceID] = sc.Count
		}
	}

	pricesWithStock := make([]domain.ProductPriceWithStock, len(p.Prices))
	for i, price := range p.Prices {
		pricesWithStock[i] = domain.ProductPriceWithStock{
			ProductPrice:   price,
			AvailableSlots: slotCountMap[price.ID],
		}
	}

	return &domain.ProductWithStock{
		Product: p,
		Prices:  pricesWithStock,
	}, nil
}
