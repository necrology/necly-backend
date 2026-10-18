package config

import (
	"strings"
	"testing"
	"time"
)

func validProductionConfig() *Config {
	return &Config{
		AppEnv:     "production",
		AppPort:    "8080",
		AppBaseURL: "https://api.necly.example",
		Database: DatabaseConfig{
			Host: "db", Port: 5432, User: "necly", Password: "a-production-only-password",
			Name: "necly", SSLMode: "verify-full", MaxOpenConns: 25, MaxIdleConns: 5,
			ConnMaxLifetime: time.Hour, ConnMaxIdleTime: 15 * time.Minute,
		},
		Redis: RedisConfig{Addr: "redis:6379", Required: true},
		JWT: JWTConfig{
			Secret: "a-unique-production-signing-secret-at-least-32-bytes", Issuer: "necly",
			AccessTTL: 15 * time.Minute, RefreshTTL: 7 * 24 * time.Hour, VerificationTTL: 24 * time.Hour,
		},
		CredentialKey: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		CORS:          CORSConfig{Origins: []string{"https://necly.example"}},
		Cookie:        CookieConfig{Secure: true, HTTPOnly: true, SameSite: "Lax", Path: "/api/v1/auth"},
		RateLimit: RateLimitConfig{
			RequestsPerMinute: 60, Burst: 10, LoginPerMinute: 5, RegisterPerHour: 5,
		},
		Payment:           PaymentConfig{Provider: "midtrans", MidtransServerKey: "configured-through-environment"},
		HTTPClientTimeout: 10 * time.Second,
	}
}

func TestValidateRejectsUnsafeProductionConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"placeholder jwt", func(c *Config) { c.JWT.Secret = "change-me-to-a-32-byte-secret-at-least" }, "JWT_SECRET"},
		{"empty database password", func(c *Config) { c.Database.Password = "" }, "DB_PASSWORD"},
		{"placeholder database password", func(c *Config) { c.Database.Password = "postgres" }, "DB_PASSWORD"},
		{"insecure database transport", func(c *Config) { c.Database.SSLMode = "disable" }, "DB_SSL_MODE"},
		{"insecure refresh cookie", func(c *Config) { c.Cookie.Secure = false }, "COOKIE_SECURE"},
		{"wildcard credentialed cors", func(c *Config) { c.CORS.Origins = []string{"*"}; c.CORS.AllowCredentials = true }, "CORS_ALLOWED_ORIGINS"},
		{"mock payment", func(c *Config) { c.Payment.Provider = "mock" }, "PAYMENT_PROVIDER"},
		{"missing inventory encryption", func(c *Config) { c.CredentialKey = "" }, "CREDENTIAL_ENCRYPTION_KEY"},
		{"smtp without tls", func(c *Config) {
			c.Email = EmailConfig{SMTPHost: "smtp.example", SMTPPort: 587, FromEmail: "noreply@necly.example", UseTLS: false}
		}, "SMTP_TLS"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validProductionConfig()
			tc.mutate(cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want mention of %s", err, tc.want)
			}
		})
	}
}

func TestValidateAcceptsConfiguredSMTPWithTLS(t *testing.T) {
	cfg := validProductionConfig()
	cfg.Email = EmailConfig{SMTPHost: "smtp.example", SMTPPort: 587, FromEmail: "noreply@necly.example", UseTLS: true}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestLoadParsesCORSAndCookieSettingsFromEnvironment(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("JWT_SECRET", "development-signing-secret-that-is-at-least-32-bytes")
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:3000, http://localhost:5173")
	t.Setenv("COOKIE_SECURE", "false")
	t.Setenv("COOKIE_HTTP_ONLY", "true")
	t.Setenv("COOKIE_SAME_SITE", "Strict")
	t.Setenv("COOKIE_PATH", "/api/v1/auth")
	t.Setenv("DB_PASSWORD", "devpassword")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.CORS.Origins) != 2 || cfg.CORS.Origins[1] != "http://localhost:5173" {
		t.Fatalf("origins = %#v", cfg.CORS.Origins)
	}
	if cfg.Cookie.Secure || !cfg.Cookie.HTTPOnly || cfg.Cookie.SameSite != "Strict" || cfg.Cookie.Path != "/api/v1/auth" {
		t.Fatalf("cookie config = %#v", cfg.Cookie)
	}
}

func TestValidateRejectsPartiallyConfiguredIntegrations(t *testing.T) {
	cfg := validProductionConfig()
	cfg.Google.ClientID = "client-id"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "GOOGLE_CLIENT_SECRET") {
		t.Fatalf("Validate() error = %v", err)
	}

	cfg = validProductionConfig()
	cfg.Storage.Endpoint = "https://s3.example"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "S3_BUCKET") {
		t.Fatalf("Validate() error = %v", err)
	}
}
