package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/handler"
	"github.com/necly/services/internal/service"
)

type archiveOrderHTTPRepository struct{ archiveErr error }

func (r *archiveOrderHTTPRepository) ListUserOrders(context.Context, string, int, int) ([]domain.Order, int64, error) {
	return nil, 0, nil
}
func (r *archiveOrderHTTPRepository) ListOrders(context.Context, service.OrderFilter, int, int) ([]domain.Order, int64, error) {
	return nil, 0, nil
}
func (r *archiveOrderHTTPRepository) GetOrder(context.Context, string, string) (*domain.Order, error) {
	return nil, service.ErrNotFound
}
func (r *archiveOrderHTTPRepository) GetOrderByID(context.Context, string) (*domain.Order, error) {
	return nil, service.ErrNotFound
}
func (r *archiveOrderHTTPRepository) GetOrderByIdempotencyKey(context.Context, string) (*domain.Order, error) {
	return nil, service.ErrNotFound
}
func (r *archiveOrderHTTPRepository) GetOrdersForAdmin(context.Context, service.OrderFilter, int, int) ([]domain.Order, int64, error) {
	return nil, 0, nil
}
func (r *archiveOrderHTTPRepository) UpdateOrderStatus(context.Context, string, domain.OrderStatus, time.Time) error {
	return nil
}
func (r *archiveOrderHTTPRepository) ArchiveOrder(context.Context, string, string, time.Time) error {
	return r.archiveErr
}

func archiveOrderTestApp(t *testing.T, archiveErr error) (*fiber.App, string) {
	t.Helper()
	const userID = "0192f0c0-0000-0000-0000-000000000001"
	const sessionID = "archive-order-session"
	tokens, err := service.NewTokenManager("handler-test-secret-at-least-32-bytes", "test", time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	authRepo := new(dummyAuthRepo)
	authSvc := service.NewAuthService(authRepo, service.NewPasswordHasher(4), tokens, service.AuthSettings{VerificationTTL: 24 * time.Hour})
	expectActiveSession(authRepo, userID, sessionID, domain.RoleCustomer)
	orderSvc := service.NewOrderService(&archiveOrderHTTPRepository{archiveErr: archiveErr}, nil, nil)
	router := handler.NewRouter(authSvc, nil, nil, nil, nil, orderSvc, nil, nil, nil, nil, nil, nil, nil, tokens, nil, nil, nil, nil)
	app := fiber.New()
	router.Register(app)
	token, err := tokens.IssueAccess(userID, string(domain.RoleCustomer), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return app, token
}

func TestArchiveOrderHTTPContract(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "archive succeeds", wantStatus: http.StatusNoContent},
		{name: "already archived succeeds", wantStatus: http.StatusNoContent},
		{name: "foreign or missing order is hidden", err: service.ErrNotFound, wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "active order is rejected", err: service.ErrOrderArchiveNotAllowed, wantStatus: http.StatusConflict, wantCode: "order_archive_not_allowed"},
		{name: "unexpected failure", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, token := archiveOrderTestApp(t, tc.err)
			req := httptest.NewRequest(http.MethodDelete, "/api/v1/orders/0192f0c0-0000-0000-0000-000000000099", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if tc.wantCode == "" {
				return
			}
			var body map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["code"] != tc.wantCode {
				t.Fatalf("code = %q, want %q", body["code"], tc.wantCode)
			}
		})
	}
}
