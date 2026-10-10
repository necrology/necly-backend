package service

import (
	"context"
	"strings"

	"github.com/necly/services/internal/domain"
)

// TestimonialStatusApproved is the only status the storefront may read. Reviews
// sit at "pending" until an admin moderates them.
const TestimonialStatusApproved = "approved"

// TestimonialRepository is the persistence slice the public testimonial feed
// needs. It is a subset of gormrepo.TestimonialRepository so tests can stub it.
type TestimonialRepository interface {
	ListTestimonials(ctx context.Context, filter TestimonialFilter, page, perPage int) ([]domain.Testimonial, int64, error)
}

// TestimonialFilter mirrors the columns the repository can filter on.
type TestimonialFilter struct {
	ProductID string
	Status    string
	Rating    int
}

// TestimonialService serves the public "Testimoni" page.
type TestimonialService struct {
	repo TestimonialRepository
}

func NewTestimonialService(repo TestimonialRepository) *TestimonialService {
	return &TestimonialService{repo: repo}
}

// ListPublic returns approved testimonials mapped to the fields the storefront
// is allowed to see. The reviewer's email and id stay in the service: the DAO
// preloads the whole user row, and serialising it would leak customer accounts
// onto a public marketing page.
func (s *TestimonialService) ListPublic(ctx context.Context, page, perPage int) ([]domain.PublicTestimonial, int64, error) {
	rows, total, err := s.repo.ListTestimonials(ctx, TestimonialFilter{Status: TestimonialStatusApproved}, page, perPage)
	if err != nil {
		return nil, 0, err
	}

	testimonials := make([]domain.PublicTestimonial, 0, len(rows))
	for _, row := range rows {
		testimonials = append(testimonials, domain.PublicTestimonial{
			ID:           row.ID,
			Title:        row.Title,
			Body:         row.Body,
			Rating:       row.Rating,
			ReviewerName: reviewerName(row),
			ProductName:  productName(row),
			CreatedAt:    row.CreatedAt,
		})
	}
	return testimonials, total, nil
}

// reviewerName falls back to a neutral label rather than an empty string so the
// card never renders an orphaned quote mark.
func reviewerName(row domain.Testimonial) string {
	if row.Reviewer != nil {
		if name := strings.TrimSpace(row.Reviewer.Name); name != "" {
			return name
		}
	}
	return "Pelanggan Necly"
}

func productName(row domain.Testimonial) string {
	if row.Product != nil {
		return strings.TrimSpace(row.Product.Name)
	}
	return ""
}
