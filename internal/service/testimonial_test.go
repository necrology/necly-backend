package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
)

type fakeTestimonialRepository struct {
	rows     []domain.Testimonial
	filter   TestimonialFilter
	page     int
	perPage  int
	listErr  error
	listCall int
}

func (r *fakeTestimonialRepository) ListTestimonials(_ context.Context, filter TestimonialFilter, page, perPage int) ([]domain.Testimonial, int64, error) {
	r.listCall++
	r.filter = filter
	r.page = page
	r.perPage = perPage
	if r.listErr != nil {
		return nil, 0, r.listErr
	}
	return r.rows, int64(len(r.rows)), nil
}

func approvedTestimonial(id, name, product string, rating int) domain.Testimonial {
	productID := "prod-" + id
	row := domain.Testimonial{
		ID:        id,
		UserID:    "usr-" + id,
		ProductID: &productID,
		Title:     "Hemat dan lancar",
		Body:      "Akses dikirim cepat, harga jauh lebih murah.",
		Rating:    rating,
		Status:    TestimonialStatusApproved,
		CreatedAt: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
	}
	if name != "" {
		row.Reviewer = &domain.User{ID: "usr-" + id, Name: name}
	}
	if product != "" {
		row.Product = &domain.Product{ID: productID, Name: product}
	}
	return row
}

// The storefront must never be handed a pending review, so the service has to
// ask the DAO for "approved" rather than filtering after the fact.
func TestListPublicAsksOnlyForApprovedTestimonials(t *testing.T) {
	repo := &fakeTestimonialRepository{rows: []domain.Testimonial{approvedTestimonial("t1", "Budi", "Netflix Premium", 5)}}
	svc := NewTestimonialService(repo)

	if _, _, err := svc.ListPublic(context.Background(), 1, 20); err != nil {
		t.Fatalf("ListPublic: %v", err)
	}
	if repo.filter.Status != TestimonialStatusApproved {
		t.Fatalf("filter status = %q, want %q", repo.filter.Status, TestimonialStatusApproved)
	}
	if repo.page != 1 || repo.perPage != 20 {
		t.Fatalf("paging = (%d, %d), want (1, 20)", repo.page, repo.perPage)
	}
}

func TestListPublicMapsReviewerAndProductNames(t *testing.T) {
	repo := &fakeTestimonialRepository{rows: []domain.Testimonial{
		approvedTestimonial("t1", "Budi Santoso", "Netflix Premium", 5),
		approvedTestimonial("t2", "Sari", "Spotify Family", 4),
	}}
	svc := NewTestimonialService(repo)

	got, total, err := svc.ListPublic(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("ListPublic: %v", err)
	}
	if total != 2 || len(got) != 2 {
		t.Fatalf("got %d rows / total %d, want 2 / 2", len(got), total)
	}
	if got[0].ReviewerName != "Budi Santoso" || got[0].ProductName != "Netflix Premium" {
		t.Fatalf("first row = %+v", got[0])
	}
	if got[0].Rating != 5 || got[1].Rating != 4 {
		t.Fatalf("ratings = %d, %d; want 5, 4", got[0].Rating, got[1].Rating)
	}
}

// A review whose account was deleted (or whose product row is gone) still has to
// render: an empty reviewer name would leave a dangling attribution on the card.
func TestListPublicFallsBackWhenAssociationsAreMissing(t *testing.T) {
	repo := &fakeTestimonialRepository{rows: []domain.Testimonial{approvedTestimonial("t1", "", "", 5)}}
	svc := NewTestimonialService(repo)

	got, _, err := svc.ListPublic(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("ListPublic: %v", err)
	}
	if got[0].ReviewerName != "Pelanggan Necly" {
		t.Fatalf("reviewer name = %q, want the neutral fallback", got[0].ReviewerName)
	}
	if got[0].ProductName != "" {
		t.Fatalf("product name = %q, want empty", got[0].ProductName)
	}
}

// The public payload is a DTO on purpose: the repository preloads whole user
// rows, and serialising those would publish customer emails.
func TestListPublicNeverExposesReviewerIdentity(t *testing.T) {
	row := approvedTestimonial("t1", "Budi", "Netflix Premium", 5)
	row.Reviewer.Email = "budi@example.com"
	repo := &fakeTestimonialRepository{rows: []domain.Testimonial{row}}
	svc := NewTestimonialService(repo)

	got, _, err := svc.ListPublic(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("ListPublic: %v", err)
	}

	if got[0].ReviewerName != "Budi" {
		t.Fatalf("reviewer name = %q", got[0].ReviewerName)
	}

	// The wire format is what actually reaches the browser, so assert on that:
	// PublicTestimonial has no email field today, and this test fails the moment
	// one is added.
	encoded, err := json.Marshal(got[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "budi@example.com") {
		t.Fatalf("public testimonial leaked the reviewer email: %s", encoded)
	}
	if strings.Contains(string(encoded), row.UserID) {
		t.Fatalf("public testimonial leaked the reviewer id: %s", encoded)
	}
}

func TestListPublicPropagatesRepositoryFailure(t *testing.T) {
	repo := &fakeTestimonialRepository{listErr: errors.New("database down")}
	svc := NewTestimonialService(repo)

	if _, _, err := svc.ListPublic(context.Background(), 1, 20); err == nil {
		t.Fatal("expected the repository error to surface")
	}
}
