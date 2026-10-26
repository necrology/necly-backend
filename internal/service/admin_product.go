package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
)

type AdminProductService struct {
	repo AdminProductRepository
}

func NewAdminProductService(repo AdminProductRepository) *AdminProductService {
	return &AdminProductService{repo: repo}
}

func (s *AdminProductService) ListProducts(ctx context.Context, filter AdminProductFilter, page, perPage int) ([]domain.Product, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	repoFilter := ProductFilter{
		CategoryID: filter.CategoryID,
		Search:     filter.Search,
		// Keep the tri-state: nil means "all products", not "inactive only".
		Active: filter.Active,
	}
	if err := ValidateProductFilter(repoFilter); err != nil {
		return nil, 0, err
	}
	return s.repo.ListProducts(ctx, repoFilter, page, perPage)
}

func (s *AdminProductService) GetProduct(ctx context.Context, id string) (*domain.Product, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	return s.repo.GetProductByID(ctx, id)
}

func (s *AdminProductService) GetProductBySlug(ctx context.Context, slug string) (*domain.Product, error) {
	return s.repo.GetProductBySlug(ctx, slug)
}

func (s *AdminProductService) CreateProduct(ctx context.Context, categoryID, name, slug, description, imageURL, iconURL string, comingSoon bool) (*domain.Product, error) {
	if categoryID == "" || name == "" || slug == "" {
		return nil, ErrInvalidInput
	}
	if err := ValidateCategoryID(categoryID); err != nil {
		return nil, err
	}
	icon, err := normalizeProductIconURL(iconURL)
	if err != nil {
		return nil, err
	}

	// Check if slug already exists. A nil error with a nil row is not a real
	// database result, but treating it as a conflict check failure would wrap
	// nil and turn a successful create into a 500.
	existing, err := s.repo.GetProductBySlug(ctx, slug)
	switch {
	case err == nil:
		if existing != nil {
			return nil, ErrConflict
		}
	case errors.Is(err, ErrNotFound):
		// Free slug.
	default:
		return nil, fmt.Errorf("check existing slug: %w", err)
	}

	now := time.Now().UTC()
	product := &domain.Product{
		ID:          uuid.NewString(),
		CategoryID:  categoryID,
		Name:        name,
		Slug:        strings.ToLower(slug),
		Description: description,
		ImageURL:    imageURL,
		IconURL:     icon,
		Active:      true,
		ComingSoon:  comingSoon,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.repo.CreateProduct(ctx, product); err != nil {
		return nil, fmt.Errorf("create product: %w", err)
	}
	return product, nil
}

func (s *AdminProductService) UpdateProduct(ctx context.Context, id, categoryID, name, slug, description string, imageURL, iconURL *string, active, comingSoon *bool) (*domain.Product, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	product, err := s.repo.GetProductByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if categoryID != "" {
		if err := ValidateCategoryID(categoryID); err != nil {
			return nil, err
		}
		product.CategoryID = categoryID
	}
	if name != "" {
		product.Name = name
	}
	if slug != "" {
		// Check if new slug is already taken. Re-submitting the product's own
		// slug is a normal edit and must not be treated as an error.
		existing, err := s.repo.GetProductBySlug(ctx, slug)
		switch {
		case err == nil:
			if existing != nil && existing.ID != id {
				return nil, ErrConflict
			}
		case errors.Is(err, ErrNotFound):
			// Free slug.
		default:
			return nil, fmt.Errorf("check existing slug: %w", err)
		}
		product.Slug = strings.ToLower(slug)
	}
	if description != "" {
		product.Description = description
	}
	if imageURL != nil {
		// A pointer distinguishes "field not sent" (preserve) from an explicit
		// empty string, which clears the product photo.
		product.ImageURL = *imageURL
	}
	if iconURL != nil {
		// Same semantics as image_url: omitted preserves, empty clears.
		icon, err := normalizeProductIconURL(*iconURL)
		if err != nil {
			return nil, err
		}
		product.IconURL = icon
	}
	if active != nil {
		product.Active = *active
	}
	if comingSoon != nil {
		product.ComingSoon = *comingSoon
	}
	// Reject the contradictory final state regardless of which field the
	// request changed: a coming-soon product must remain active so the
	// catalog can show it while checkout refuses it.
	if product.ComingSoon && !product.Active {
		return nil, fmt.Errorf("%w: produk coming soon harus aktif", ErrInvalidInput)
	}

	product.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateProduct(ctx, product); err != nil {
		return nil, fmt.Errorf("update product: %w", err)
	}
	return product, nil
}

func (s *AdminProductService) DeleteProduct(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	now := time.Now().UTC()
	return s.repo.DeleteProduct(ctx, id, now)
}

func (s *AdminProductService) CreateProductPrice(ctx context.Context, productID, durationID string, amount int64, currency string) (*domain.ProductPrice, error) {
	if productID == "" || durationID == "" || amount < 0 {
		return nil, ErrInvalidInput
	}
	if _, err := uuid.Parse(productID); err != nil {
		return nil, ErrInvalidInput
	}
	if _, err := uuid.Parse(durationID); err != nil {
		return nil, ErrInvalidInput
	}

	now := time.Now().UTC()
	price := &domain.ProductPrice{
		ID:         uuid.NewString(),
		ProductID:  productID,
		DurationID: durationID,
		Amount:     amount,
		Currency:   currency,
		Active:     true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := s.repo.CreateProductPrice(ctx, price); err != nil {
		return nil, fmt.Errorf("create product price: %w", err)
	}
	return price, nil
}

func (s *AdminProductService) UpdateProductPrice(ctx context.Context, id string, amount *int64, active *bool) (*domain.ProductPrice, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	price, err := s.repo.GetProductPriceByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if amount != nil {
		if *amount < 0 {
			return nil, ErrInvalidInput
		}
		price.Amount = *amount
	}
	if active != nil {
		price.Active = *active
	}

	price.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateProductPrice(ctx, price); err != nil {
		return nil, fmt.Errorf("update product price: %w", err)
	}
	return price, nil
}

func (s *AdminProductService) DeleteProductPrice(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	return s.repo.DeleteProductPrice(ctx, id)
}

// maxProductIconDataURLLength is the hard cap for an uploaded icon. The
// frontend compresses the image before sending it, so a larger payload is
// rejected instead of being written to a text column.
const maxProductIconDataURLLength = 512 * 1024

var productIconDataURLPrefixes = []string{
	"data:image/jpeg;base64,",
	"data:image/png;base64,",
	"data:image/webp;base64,",
}

// normalizeProductIconURL trims the value and accepts an absolute storefront
// path, an http(s) URL, or a base64 data URL for a raster image. Other data
// URLs (notably SVG and text/html) and other schemes (javascript:, a relative
// path, a protocol-relative //host) are rejected so the field cannot smuggle a
// value the frontend would not render as a safe image.
func normalizeProductIconURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "data:") {
		return normalizeProductIconDataURL(value, lower)
	}
	if strings.HasPrefix(value, "/") {
		if strings.HasPrefix(value, "//") {
			return "", fmt.Errorf("%w: icon_url harus path absolut, URL http/https, atau data URL gambar", ErrInvalidInput)
		}
		return value, nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("%w: icon_url harus path absolut, URL http/https, atau data URL gambar", ErrInvalidInput)
	}
	return value, nil
}

func normalizeProductIconDataURL(value, lower string) (string, error) {
	if len(value) > maxProductIconDataURLLength {
		return "", fmt.Errorf("%w: icon_url melebihi batas 512 KB", ErrInvalidInput)
	}
	var matched string
	for _, prefix := range productIconDataURLPrefixes {
		if strings.HasPrefix(lower, prefix) {
			matched = prefix
			break
		}
	}
	if matched == "" {
		return "", fmt.Errorf("%w: data URL icon harus JPEG, PNG, atau WebP base64", ErrInvalidInput)
	}
	payload := value[len(matched):]
	if payload == "" {
		return "", fmt.Errorf("%w: data URL icon tidak berisi data", ErrInvalidInput)
	}
	// Accept padded and unpadded base64. The decoded bytes are not inspected
	// here: the MIME type comes from the prefix, and the browser renders it as
	// an <img> source, never as executable markup.
	if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
		if _, rawErr := base64.RawStdEncoding.DecodeString(payload); rawErr != nil {
			return "", fmt.Errorf("%w: data URL icon bukan base64 yang valid", ErrInvalidInput)
		}
	}
	return value, nil
}
