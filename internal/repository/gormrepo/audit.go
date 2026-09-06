package gormrepo

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
)

type AuditRepository interface {
	ListAuditLogs(ctx context.Context, filter service.AuditLogFilter, page, perPage int) ([]domain.AuditLog, int64, error)
	CreateAuditLog(ctx context.Context, log *domain.AuditLog) error
}

func (s *Store) ListAuditLogs(ctx context.Context, filter service.AuditLogFilter, page, perPage int) ([]domain.AuditLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 200 {
		perPage = 50
	}
	query := s.db.WithContext(ctx).Model(&domain.AuditLog{}).Preload("ActorUser")
	if filter.UserID != "" {
		query = query.Where("actor_user_id = ?", filter.UserID)
	}
	if filter.Action != "" {
		query = query.Where("action = ?", filter.Action)
	}
	if filter.Entity != "" {
		query = query.Where("resource_type = ?", filter.Entity)
	}
	if filter.EntityID != "" {
		query = query.Where("resource_id = ?", filter.EntityID)
	}
	if filter.FromDate != "" {
		if t, err := time.Parse("2006-01-02", filter.FromDate); err == nil {
			query = query.Where("created_at >= ?", t)
		}
	}
	if filter.ToDate != "" {
		if t, err := time.Parse("2006-01-02", filter.ToDate); err == nil {
			query = query.Where("created_at <= ?", t)
		}
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count audit logs: %w", err)
	}
	var logs []domain.AuditLog
	offset := (page - 1) * perPage
	if err := query.Order("created_at DESC").Offset(offset).Limit(perPage).Find(&logs).Error; err != nil {
		return nil, 0, fmt.Errorf("list audit logs: %w", err)
	}
	return logs, total, nil
}

func (s *Store) CreateAuditLog(ctx context.Context, log *domain.AuditLog) error {
	log.ID = uuid.NewString()
	log.CreatedAt = s.nowUTC()
	return s.db.WithContext(ctx).Create(log).Error
}
