package gormrepo

import (
	"context"
	"errors"
	"fmt"
	"sort"
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

// translateCatalogWriteError maps database constraint failures onto the shared
// service taxonomy. Without it a duplicate slug races past the cheap existence
// check and surfaces as a 500; an unknown category reference does the same.
func translateCatalogWriteError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, gorm.ErrDuplicatedKey):
		return service.ErrConflict
	case errors.Is(err, gorm.ErrForeignKeyViolated):
		return service.ErrInvalidInput
	default:
		return err
	}
}

func activePricePreload(db *gorm.DB) *gorm.DB {
	return db.Where("active = ?", true)
}

func applyProductFilters(query *gorm.DB, filter service.ProductFilter) *gorm.DB {
	query = query.Where("deleted_at IS NULL")
	if filter.Active != nil {
		query = query.Where("active = ?", *filter.Active)
	}
	if filter.CategoryID != "" {
		query = query.Where("category_id = ?", filter.CategoryID)
	}
	if filter.Search != "" {
		like := "%" + strings.ToLower(filter.Search) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(description) LIKE ?", like, like)
	}
	if filter.MinPrice != nil || filter.MaxPrice != nil {
		// Both bounds must hold for the same active price row. Two separate
		// EXISTS clauses would let a product pass when its cheapest price is
		// below max and a different expensive price is above min, even though no
		// single price sits inside the requested range.
		predicate := "EXISTS (SELECT 1 FROM product_prices pp WHERE pp.product_id = products.id AND pp.active = TRUE"
		args := make([]any, 0, 2)
		if filter.MinPrice != nil {
			predicate += " AND pp.amount >= ?"
			args = append(args, *filter.MinPrice)
		}
		if filter.MaxPrice != nil {
			predicate += " AND pp.amount <= ?"
			args = append(args, *filter.MaxPrice)
		}
		predicate += ")"
		query = query.Where(predicate, args...)
	}
	if filter.InStock {
		query = query.Where("EXISTS (SELECT 1 FROM inventory_slots s JOIN product_prices pp ON pp.id = s.product_price_id WHERE pp.product_id = products.id AND pp.active = TRUE AND s.status = 'available')")
	}
	return query
}

func sortPricesByDuration(prices []domain.ProductPrice) {
	sort.SliceStable(prices, func(i, j int) bool {
		leftDays, rightDays := 0, 0
		if prices[i].Duration != nil {
			leftDays = prices[i].Duration.Days
		}
		if prices[j].Duration != nil {
			rightDays = prices[j].Duration.Days
		}
		if leftDays != rightDays {
			return leftDays < rightDays
		}
		return prices[i].Amount < prices[j].Amount
	})
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
	if _, err := uuid.Parse(id); err != nil {
		return nil, service.ErrNotFound
	}
	var cat domain.Category
	err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", id).First(&cat).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &cat, err
}

func (s *Store) GetCategoryBySlug(ctx context.Context, slug string) (*domain.Category, error) {
	var cat domain.Category
	err := s.db.WithContext(ctx).Where("slug = ? AND deleted_at IS NULL", slug).First(&cat).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &cat, err
}

func (s *Store) CreateCategory(ctx context.Context, c *domain.Category) error {
	c.ID = uuid.NewString()
	c.CreatedAt = s.nowUTC()
	c.UpdatedAt = c.CreatedAt
	if err := s.db.WithContext(ctx).Create(c).Error; err != nil {
		return translateCatalogWriteError(err)
	}
	return nil
}

func (s *Store) UpdateCategory(ctx context.Context, c *domain.Category) error {
	if _, err := uuid.Parse(c.ID); err != nil {
		return service.ErrNotFound
	}
	c.UpdatedAt = s.nowUTC()
	result := s.db.WithContext(ctx).Model(&domain.Category{}).
		Where("id = ? AND deleted_at IS NULL", c.ID).
		Select("*").Omit("id", "created_at").
		Updates(c)
	if result.Error != nil {
		return translateCatalogWriteError(result.Error)
	}
	if result.RowsAffected == 0 {
		return service.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteCategory(ctx context.Context, id string, now time.Time) error {
	if _, err := uuid.Parse(id); err != nil {
		return service.ErrNotFound
	}
	result := s.db.WithContext(ctx).Model(&domain.Category{}).
		Where("id = ? AND deleted_at IS NULL", id).
		Updates(map[string]any{"deleted_at": now, "updated_at": now})
	if result.Error != nil {
		return fmt.Errorf("delete category: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return service.ErrNotFound
	}
	return nil
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
	query := applyProductFilters(
		s.db.WithContext(ctx).Model(&domain.Product{}).Preload("Category").Preload("Prices.Duration"),
		filter,
	)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count products: %w", err)
	}
	var products []domain.Product
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC, id ASC").Offset(offset).Limit(perPage).Find(&products).Error; err != nil {
		return nil, 0, fmt.Errorf("list products: %w", err)
	}
	for index := range products {
		sortPricesByDuration(products[index].Prices)
	}
	return products, total, nil
}

func (s *Store) GetProductByID(ctx context.Context, id string) (*domain.Product, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, service.ErrNotFound
	}
	var p domain.Product
	err := s.db.WithContext(ctx).Preload("Category").Preload("Prices.Duration").
		Where("id = ? AND deleted_at IS NULL", id).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &p, err
}

func (s *Store) GetProductBySlug(ctx context.Context, slug string) (*domain.Product, error) {
	var p domain.Product
	err := s.db.WithContext(ctx).Preload("Category").Preload("Prices.Duration").
		Where("slug = ? AND deleted_at IS NULL", slug).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	return &p, err
}

func (s *Store) CreateProduct(ctx context.Context, p *domain.Product) error {
	p.ID = uuid.NewString()
	p.CreatedAt = s.nowUTC()
	p.UpdatedAt = p.CreatedAt
	if err := s.db.WithContext(ctx).Create(p).Error; err != nil {
		return translateCatalogWriteError(err)
	}
	return nil
}

func (s *Store) UpdateProduct(ctx context.Context, p *domain.Product) error {
	if _, err := uuid.Parse(p.ID); err != nil {
		return service.ErrNotFound
	}
	p.UpdatedAt = s.nowUTC()
	result := s.db.WithContext(ctx).Model(&domain.Product{}).
		Where("id = ? AND deleted_at IS NULL", p.ID).
		Select("*").Omit("id", "created_at").
		Updates(p)
	if result.Error != nil {
		return translateCatalogWriteError(result.Error)
	}
	if result.RowsAffected == 0 {
		return service.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteProduct(ctx context.Context, id string, now time.Time) error {
	if _, err := uuid.Parse(id); err != nil {
		return service.ErrNotFound
	}
	result := s.db.WithContext(ctx).Model(&domain.Product{}).
		Where("id = ? AND deleted_at IS NULL", id).
		Updates(map[string]any{"deleted_at": now, "updated_at": now})
	if result.Error != nil {
		return fmt.Errorf("delete product: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return service.ErrNotFound
	}
	return nil
}

func (s *Store) ListProductPrices(ctx context.Context, productID string, activeOnly bool) ([]domain.ProductPrice, error) {
	var prices []domain.ProductPrice
	query := s.db.WithContext(ctx).Preload("Duration").Where("product_id = ?", productID)
	if activeOnly {
		query = query.Where("active = ?", true)
	}
	if err := query.Order("amount ASC").Find(&prices).Error; err != nil {
		return nil, fmt.Errorf("list product prices: %w", err)
	}
	if activeOnly {
		sortPricesByDuration(prices)
	}
	return prices, nil
}

func (s *Store) GetProductPriceByID(ctx context.Context, id string) (*domain.ProductPrice, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, service.ErrNotFound
	}
	var price domain.ProductPrice
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&price).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
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
	if err := s.db.WithContext(ctx).Create(p).Error; err != nil {
		return translateCatalogWriteError(err)
	}
	return nil
}

func (s *Store) UpdateProductPrice(ctx context.Context, p *domain.ProductPrice) error {
	if _, err := uuid.Parse(p.ID); err != nil {
		return service.ErrNotFound
	}
	p.UpdatedAt = s.nowUTC()
	result := s.db.WithContext(ctx).Model(&domain.ProductPrice{}).
		Where("id = ?", p.ID).
		Select("*").Omit("id", "created_at", "product_id").
		Updates(p)
	if result.Error != nil {
		return translateCatalogWriteError(result.Error)
	}
	if result.RowsAffected == 0 {
		return service.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteProductPrice(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return service.ErrNotFound
	}
	result := s.db.WithContext(ctx).Delete(&domain.ProductPrice{}, "id = ?", id)
	if result.Error != nil {
		return translateCatalogWriteError(result.Error)
	}
	if result.RowsAffected == 0 {
		return service.ErrNotFound
	}
	return nil
}

func (s *Store) ListProductDurations(ctx context.Context) ([]domain.ProductDuration, error) {
	var durations []domain.ProductDuration
	if err := s.db.WithContext(ctx).Order("days ASC, name ASC").Find(&durations).Error; err != nil {
		return nil, fmt.Errorf("list product durations: %w", err)
	}
	return durations, nil
}

func (s *Store) ListProductsWithStock(ctx context.Context, filter service.ProductFilter, page, perPage int) ([]domain.ProductWithStock, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}

	query := applyProductFilters(
		s.db.WithContext(ctx).Model(&domain.Product{}).
			Preload("Category").
			Preload("Prices", activePricePreload).
			Preload("Prices.Duration"),
		filter,
	)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count products: %w", err)
	}

	var products []domain.Product
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC, id ASC").Offset(offset).Limit(perPage).Find(&products).Error; err != nil {
		return nil, 0, fmt.Errorf("list products: %w", err)
	}

	priceIDs := make([]string, 0)
	for index := range products {
		sortPricesByDuration(products[index].Prices)
		for _, price := range products[index].Prices {
			priceIDs = append(priceIDs, price.ID)
		}
	}

	slotCountMap := map[string]int{}
	if len(priceIDs) > 0 {
		type slotCount struct {
			ProductPriceID string
			Count          int
		}
		var slotCounts []slotCount
		if err := s.db.WithContext(ctx).Model(&domain.InventorySlot{}).
			Select("product_price_id, COUNT(*) as count").
			Where("product_price_id IN ? AND status = ?", priceIDs, "available").
			Group("product_price_id").
			Scan(&slotCounts).Error; err != nil {
			return nil, 0, fmt.Errorf("count available slots: %w", err)
		}
		for _, count := range slotCounts {
			slotCountMap[count.ProductPriceID] = count.Count
		}
	}

	results := make([]domain.ProductWithStock, len(products))
	for index, product := range products {
		prices := make([]domain.ProductPriceWithStock, len(product.Prices))
		for priceIndex, price := range product.Prices {
			prices[priceIndex] = domain.ProductPriceWithStock{
				ProductPrice:   price,
				AvailableSlots: slotCountMap[price.ID],
			}
		}
		results[index] = domain.ProductWithStock{Product: product, Prices: prices}
	}

	return results, total, nil
}

func (s *Store) GetProductWithStock(ctx context.Context, id string) (*domain.ProductWithStock, error) {
	// Kolom products.id bertipe uuid. Pemanggil mencoba id lalu slug, jadi slug
	// yang sah pun sampai ke sini dan Postgres menjawabnya dengan SQLSTATE 22P02
	// — galat yang tercatat di log untuk setiap permintaan slug. Bentuk id
	// diperiksa lebih dulu; hasilnya sama saja, tidak ada produk dengan id
	// seperti itu.
	if _, err := uuid.Parse(id); err != nil {
		return nil, service.ErrNotFound
	}

	var p domain.Product
	err := s.db.WithContext(ctx).
		Preload("Category").
		Preload("Prices", activePricePreload).
		Preload("Prices.Duration").
		Where("id = ? AND deleted_at IS NULL AND active = TRUE", id).
		First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get product: %w", err)
	}
	return s.productWithStock(ctx, p)
}

func (s *Store) GetProductBySlugWithStock(ctx context.Context, slug string) (*domain.ProductWithStock, error) {
	var p domain.Product
	err := s.db.WithContext(ctx).
		Preload("Category").
		Preload("Prices", activePricePreload).
		Preload("Prices.Duration").
		Where("slug = ? AND deleted_at IS NULL AND active = TRUE", slug).
		First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get product by slug: %w", err)
	}
	return s.productWithStock(ctx, p)
}

func (s *Store) productWithStock(ctx context.Context, p domain.Product) (*domain.ProductWithStock, error) {
	sortPricesByDuration(p.Prices)

	priceIDs := make([]string, 0, len(p.Prices))
	for _, price := range p.Prices {
		priceIDs = append(priceIDs, price.ID)
	}

	slotCountMap := map[string]int{}
	if len(priceIDs) > 0 {
		type slotCount struct {
			ProductPriceID string
			Count          int
		}
		var slotCounts []slotCount
		if err := s.db.WithContext(ctx).Model(&domain.InventorySlot{}).
			Select("product_price_id, COUNT(*) as count").
			Where("product_price_id IN ? AND status = ?", priceIDs, "available").
			Group("product_price_id").
			Scan(&slotCounts).Error; err != nil {
			return nil, fmt.Errorf("count available slots: %w", err)
		}
		for _, count := range slotCounts {
			slotCountMap[count.ProductPriceID] = count.Count
		}
	}

	pricesWithStock := make([]domain.ProductPriceWithStock, len(p.Prices))
	for index, price := range p.Prices {
		pricesWithStock[index] = domain.ProductPriceWithStock{
			ProductPrice:   price,
			AvailableSlots: slotCountMap[price.ID],
		}
	}

	return &domain.ProductWithStock{Product: p, Prices: pricesWithStock}, nil
}
