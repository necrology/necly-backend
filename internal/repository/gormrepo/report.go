package gormrepo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
	"gorm.io/gorm"
)

// ReportRepository is the persistence side of the "Laporan Kendala" flow.
type ReportRepository interface {
	CreateIssueReport(ctx context.Context, report *domain.IssueReport) error
	LatestIssueReportByEmail(ctx context.Context, email string) (*domain.IssueReport, error)
	OrderMatchesEmail(ctx context.Context, number, email string) (bool, error)
}

func (s *Store) CreateIssueReport(ctx context.Context, report *domain.IssueReport) error {
	if err := s.db.WithContext(ctx).Create(report).Error; err != nil {
		return fmt.Errorf("create issue report: %w", err)
	}
	return nil
}

func (s *Store) LatestIssueReportByEmail(ctx context.Context, email string) (*domain.IssueReport, error) {
	var report domain.IssueReport
	err := s.db.WithContext(ctx).
		Where("LOWER(email) = LOWER(?)", strings.TrimSpace(email)).
		Order("created_at DESC").
		First(&report).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, service.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("latest issue report: %w", err)
	}
	return &report, nil
}

// OrderMatchesEmail reports whether the order number exists and was placed by
// the given email. Both mismatches answer the same way on purpose: the public
// form must not reveal which of the two was wrong.
func (s *Store) OrderMatchesEmail(ctx context.Context, number, email string) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).
		Model(&domain.Order{}).
		Joins("JOIN users ON users.id = orders.user_id").
		Where("LOWER(orders.number) = LOWER(?) AND LOWER(users.email) = LOWER(?)",
			strings.TrimSpace(number), strings.TrimSpace(email)).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("match order and email: %w", err)
	}
	return count > 0, nil
}
