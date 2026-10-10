package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/helmet"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/necly/services/internal/config"
	"github.com/necly/services/internal/handler"
	"github.com/necly/services/internal/platform/database"
	"github.com/necly/services/internal/platform/mailer"
	"github.com/necly/services/internal/platform/rediscache"
	"github.com/necly/services/internal/repository/gormrepo"
	"github.com/necly/services/internal/service"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}

	db, err := database.Open(database.Config{
		Host:            cfg.Database.Host,
		Port:            cfg.Database.Port,
		User:            cfg.Database.User,
		Password:        cfg.Database.Password,
		Name:            cfg.Database.Name,
		SSLMode:         cfg.Database.SSLMode,
		MaxOpenConns:    cfg.Database.MaxOpenConns,
		MaxIdleConns:    cfg.Database.MaxIdleConns,
		ConnMaxLifetime: cfg.Database.ConnMaxLifetime,
		ConnMaxIdleTime: cfg.Database.ConnMaxIdleTime,
	})
	if err != nil {
		log.Fatalf("database connection failed: %v", err)
	}
	// Skip AutoMigrate - using SQL migrations instead

	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if cfg.Redis.Addr != "" {
		if err := redisClient.Ping(ctx).Err(); err != nil {
			log.Printf("redis connection warning: %v", err)
			if cfg.Redis.Required {
				log.Fatalf("redis required but unavailable: %v", err)
			}
			redisClient = nil
		}
	} else {
		log.Println("redis disabled (REDIS_ADDR not set)")
		redisClient = nil
	}

	tokenManager, err := service.NewTokenManager(cfg.JWT.Secret, cfg.JWT.Issuer, cfg.JWT.AccessTTL, cfg.JWT.RefreshTTL)
	if err != nil {
		log.Fatalf("token manager: %v", err)
	}

	hasher := service.NewPasswordHasher(4)
	cipher, err := service.NewCredentialCipher(cfg.CredentialKey)
	if err != nil && cfg.CredentialKey != "" {
		log.Fatalf("credential cipher: %v", err)
	}

	store := gormrepo.NewStore(db)
	googleConfig := &service.GoogleOAuthConfig{
		ClientID:          cfg.Google.ClientID,
		ClientSecret:      cfg.Google.ClientSecret,
		RedirectURL:       cfg.Google.RedirectURL,
		FrontendReturnURL: cfg.Google.FrontendReturnURL,
		AuthURL:           cfg.Google.AuthURL,
		TokenURL:          cfg.Google.TokenURL,
		UserInfoURL:       cfg.Google.UserInfoURL,
		StateTTL:          cfg.Google.StateTTL,
	}
	// mailer.New warns loudly and falls back to a log-only sender when SMTP_HOST
	// is empty, so a deployment without mail credentials still runs.
	mail := mailer.New(mailer.Config{
		Host: cfg.Email.SMTPHost, Port: cfg.Email.SMTPPort, User: cfg.Email.SMTPUser,
		Password: cfg.Email.SMTPPassword, From: cfg.Email.FromEmail, FromName: cfg.Email.FromName,
		UseTLS: cfg.Email.UseTLS,
	})
	authSvc := service.NewAuthService(store, hasher, tokenManager, service.AuthSettings{
		VerificationTTL:  cfg.JWT.VerificationTTL,
		PasswordResetTTL: cfg.PasswordResetTTL,
		FrontendBaseURL:  cfg.AppFrontendURL,
		Mailer:           mail,
		Google:           googleConfig,
	})
	catalogSvc := service.NewCatalogService(store)
	cartSvc := service.NewCartService(store)
	orderSvc := service.NewOrderService(store, store, store)
	reportSvc := service.NewReportService(store, rediscache.NewCooldown(redisClient, "report:cooldown:", service.ReportCooldown), mail, cfg.ReportAdminEmail)

	// Create payment provider registry
	paymentConfig := map[string]string{
		"MIDTRANS_SERVER_KEY":    cfg.Payment.MidtransServerKey,
		"MIDTRANS_CLIENT_KEY":    cfg.Payment.MidtransClientKey,
		"MIDTRANS_IS_PRODUCTION": fmt.Sprintf("%v", cfg.Payment.MidtransIsProduction),
		"XENDIT_CALLBACK_TOKEN":  cfg.Payment.XenditCallbackToken,
		"XENDIT_API_KEY":         cfg.Payment.XenditAPIKey,
		"XENDIT_IS_PRODUCTION":   fmt.Sprintf("%v", cfg.Payment.XenditIsProduction),
	}
	providerRegistry := service.CreatePaymentProviders(paymentConfig, cfg.HTTPClientTimeout)

	// Select provider from config
	var checkoutProvider service.PaymentProvider
	if cfg.Payment.Provider != "" && cfg.Payment.Provider != "mock" {
		if p, err := providerRegistry.Get(cfg.Payment.Provider); err == nil {
			checkoutProvider = p
		}
	}
	if checkoutProvider == nil {
		// Fallback to mock only in non-production
		if cfg.AppEnv != "production" {
			checkoutProvider = &mockProvider{}
		} else {
			log.Fatalf("no payment provider configured for production: PAYMENT_PROVIDER=%q", cfg.Payment.Provider)
		}
	}

	checkoutSvc := service.NewCheckoutService(store, checkoutProvider, cfg.JWT.AccessTTL)
	webhookSvc := service.NewWebhookService(store, providerRegistry)

	adminSvc := service.NewAdminDashboardService(store)
	adminUserSvc := service.NewAdminUserService(store)
	adminProductSvc := service.NewAdminProductService(store)
	adminCategorySvc := service.NewAdminCategoryService(store)
	adminOrderSvc := service.NewAdminOrderService(store)
	adminVoucherSvc := service.NewAdminVoucherService(store)
	adminInventorySvc := service.NewAdminInventoryService(store, cipher)

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return c.Status(code).JSON(fiber.Map{"error": err.Error()})
		},
		DisableStartupMessage: true,
	})

	app.Use(recover.New())
	app.Use(apiSecurityHeaders())
	app.Use(compress.New())
	app.Use(logger.New(logger.Config{Format: "[${time}] ${status} ${method} ${path} ${latency}\n"}))
	app.Use(apiCORS(cfg.CORS))

	app.Use(limiter.New(limiter.Config{
		Max:        cfg.RateLimit.RequestsPerMinute,
		Expiration: time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "rate limit exceeded"})
		},
	}))

	router := handler.NewRouter(authSvc, catalogSvc, cartSvc, checkoutSvc, webhookSvc, orderSvc, adminSvc, adminUserSvc, adminProductSvc, adminCategorySvc, adminOrderSvc, adminVoucherSvc, adminInventorySvc, tokenManager, cipher, redisClient, reportSvc)
	router.Register(app)

	go func() {
		if err := app.Listen(":" + cfg.AppPort); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		log.Fatalf("graceful shutdown failed: %v", err)
	}
	log.Println("server stopped")
}

func apiSecurityHeaders() fiber.Handler {
	return helmet.New(helmet.Config{
		CrossOriginResourcePolicy: "cross-origin",
	})
}

func apiCORS(cfg config.CORSConfig) fiber.Handler {
	return cors.New(cors.Config{
		AllowOrigins:     strings.Join(cfg.Origins, ","),
		AllowMethods:     "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders:     "Origin,Content-Type,Accept,Authorization,X-CSRF-Token,Idempotency-Key",
		AllowCredentials: cfg.AllowCredentials,
		MaxAge:           86400,
	})
}

type mockProvider struct{}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) CreatePayment(ctx context.Context, req service.PaymentRequest) (*service.PaymentInitiation, error) {
	return &service.PaymentInitiation{
		ProviderPaymentID: "pay_" + req.OrderID,
		Status:            "pending",
		CheckoutURL:       fmt.Sprintf("https://pay.mock/%s", req.OrderID),
	}, nil
}
func (m *mockProvider) VerifyWebhook(headers map[string]string, body []byte) (*service.PaymentEvent, error) {
	return nil, nil
}
