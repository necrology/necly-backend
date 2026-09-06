package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
)

type AdminInventoryService struct {
	repo   InventoryRepository
	cipher *CredentialCipher
}

func NewAdminInventoryService(repo InventoryRepository, cipher *CredentialCipher) *AdminInventoryService {
	return &AdminInventoryService{repo: repo, cipher: cipher}
}

type InventoryAccountFilter struct {
	ProductID string
	Active    *bool
}

type CreateInventoryAccountInput struct {
	ProductID   string `json:"product_id"`
	Credentials string `json:"credentials"`
}

type UpdateInventoryAccountInput struct {
	Credentials *string    `json:"credentials"`
	Status      *string    `json:"status"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

func (s *AdminInventoryService) ListInventoryAccounts(ctx context.Context, filter InventoryAccountFilter, page, perPage int) ([]domain.InventoryAccount, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	accounts, total, err := s.repo.ListAccounts(ctx, page, perPage, filter.ProductID)
	if err != nil {
		return nil, 0, err
	}
	if filter.Active != nil {
		filtered := accounts[:0]
		for _, a := range accounts {
			if (a.Status == "active") == *filter.Active {
				filtered = append(filtered, a)
			}
		}
		accounts = filtered
		total = int64(len(accounts))
	}
	return accounts, total, nil
}

func (s *AdminInventoryService) CreateInventoryAccount(ctx context.Context, input CreateInventoryAccountInput) (*domain.InventoryAccount, error) {
	if input.ProductID == "" || input.Credentials == "" {
		return nil, ErrInvalidInput
	}

	if s.cipher == nil {
		return nil, errors.New("credential cipher not configured")
	}
	encrypted, err := s.cipher.Encrypt([]byte(input.Credentials))
	if err != nil {
		return nil, fmt.Errorf("encrypt credentials: %w", err)
	}

	now := time.Now().UTC()
	account := &domain.InventoryAccount{
		ID:                   uuid.NewString(),
		ProductID:            input.ProductID,
		EncryptedCredentials: encrypted,
		Status:               "active",
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	if err := s.repo.CreateAccount(ctx, account); err != nil {
		return nil, fmt.Errorf("create inventory account: %w", err)
	}
	return account, nil
}

func (s *AdminInventoryService) UpdateInventoryAccount(ctx context.Context, id string, input UpdateInventoryAccountInput) (*domain.InventoryAccount, error) {
	account, err := s.repo.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}

	if input.Credentials != nil {
		if s.cipher == nil {
			return nil, errors.New("credential cipher not configured")
		}
		encrypted, err := s.cipher.Encrypt([]byte(*input.Credentials))
		if err != nil {
			return nil, fmt.Errorf("encrypt credentials: %w", err)
		}
		account.EncryptedCredentials = encrypted
	}
	if input.Status != nil {
		account.Status = *input.Status
	}
	if input.ExpiresAt != nil {
		account.ExpiresAt = input.ExpiresAt
	}

	account.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateAccount(ctx, account); err != nil {
		return nil, fmt.Errorf("update inventory account: %w", err)
	}
	return account, nil
}

func (s *AdminInventoryService) GetInventoryAccount(ctx context.Context, id string) (*domain.InventoryAccount, error) {
	return s.repo.GetAccount(ctx, id)
}

func (s *AdminInventoryService) DeleteInventoryAccount(ctx context.Context, id string) error {
	return s.repo.DeleteAccount(ctx, id)
}
