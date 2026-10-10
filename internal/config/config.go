package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains configuration loaded exclusively from environment variables.
// Secret fields must never be logged or serialized.
type Config struct {
	AppEnv     string
	AppPort    string
	AppBaseURL string
	// AppFrontendURL is the storefront origin. Links a human opens (the
	// password reset page) point here; AppBaseURL is this API's own origin.
	AppFrontendURL string
	// PasswordResetTTL bounds how long a "lupa kata sandi" link stays valid.
	PasswordResetTTL time.Duration
	// ReportAdminEmail receives a notification for every issue report. When it
	// is empty the notification is skipped and the report is only stored.
	ReportAdminEmail  string
	Database          DatabaseConfig
	Redis             RedisConfig
	JWT               JWTConfig
	CredentialKey     string
	RateLimit         RateLimitConfig
	Payment           PaymentConfig
	Email             EmailConfig
	Google            GoogleConfig
	Storage           StorageConfig
	CORS              CORSConfig
	Cookie            CookieConfig
	HTTPClientTimeout time.Duration
}

type DatabaseConfig struct {
	Host             string
	Port             int
	User             string
	Password         string
	Name             string
	SSLMode          string
	AllowInsecureTLS bool
	MaxOpenConns     int
	MaxIdleConns     int
	ConnMaxLifetime  time.Duration
	ConnMaxIdleTime  time.Duration
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
	Required bool
}

type JWTConfig struct {
	Secret          string
	Issuer          string
	AccessTTL       time.Duration
	RefreshTTL      time.Duration
	VerificationTTL time.Duration
}

type RateLimitConfig struct {
	RequestsPerMinute int
	Burst             int
	LoginPerMinute    int
	RegisterPerHour   int
}

type PaymentConfig struct {
	Provider             string
	MidtransServerKey    string
	MidtransClientKey    string
	MidtransBaseURL      string
	MidtransIsProduction bool
	XenditAPIKey         string
	XenditCallbackToken  string
	XenditBaseURL        string
	XenditIsProduction   bool
	Sandbox              bool
}

type EmailConfig struct {
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	FromEmail    string
	FromName     string
	UseTLS       bool
}

type GoogleConfig struct {
	ClientID          string
	ClientSecret      string
	RedirectURL       string
	FrontendReturnURL string
	AuthURL           string
	TokenURL          string
	UserInfoURL       string
	StateTTL          time.Duration
}

type StorageConfig struct {
	Endpoint       string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	PublicBaseURL  string
	UsePathStyle   bool
	MaxUploadBytes int64
	PresignTTL     time.Duration
}

type CORSConfig struct {
	Origins          []string
	AllowCredentials bool
}

type CookieConfig struct {
	Secure   bool
	HTTPOnly bool
	SameSite string
	Domain   string
	Path     string
}

// Load parses and validates environment configuration. It deliberately has no
// built-in signing key or database password.
func Load() (*Config, error) {
	env := strings.ToLower(strings.TrimSpace(getEnv("APP_ENV", "development")))
	production := env == "production"

	dbPort, err := envInt("DB_PORT", 5432)
	if err != nil {
		return nil, err
	}
	maxOpen, err := envInt("DB_MAX_OPEN_CONNS", 25)
	if err != nil {
		return nil, err
	}
	maxIdle, err := envInt("DB_MAX_IDLE_CONNS", 5)
	if err != nil {
		return nil, err
	}
	redisDB, err := envInt("REDIS_DB", 0)
	if err != nil {
		return nil, err
	}
	rpm, err := envInt("RATE_LIMIT_RPM", 60)
	if err != nil {
		return nil, err
	}
	burst, err := envInt("RATE_LIMIT_BURST", 10)
	if err != nil {
		return nil, err
	}
	loginRPM, err := envInt("LOGIN_RATE_LIMIT_RPM", 5)
	if err != nil {
		return nil, err
	}
	registerRPH, err := envInt("REGISTER_RATE_LIMIT_RPH", 5)
	if err != nil {
		return nil, err
	}
	smtpPort, err := envInt("SMTP_PORT", 587)
	if err != nil {
		return nil, err
	}
	maxUpload, err := envInt64("S3_MAX_UPLOAD_BYTES", 10<<20)
	if err != nil {
		return nil, err
	}

	cookieSecure, err := envBool("COOKIE_SECURE", production)
	if err != nil {
		return nil, err
	}
	cookieHTTPOnly, err := envBool("COOKIE_HTTP_ONLY", true)
	if err != nil {
		return nil, err
	}
	allowCredentials, err := envBool("CORS_ALLOW_CREDENTIALS", true)
	if err != nil {
		return nil, err
	}
	paymentSandbox, err := envBool("PAYMENT_SANDBOX", !production)
	if err != nil {
		return nil, err
	}
	redisRequired, err := envBool("REDIS_REQUIRED", production)
	if err != nil {
		return nil, err
	}
	dbAllowInsecure, err := envBool("DB_ALLOW_INSECURE_TLS", false)
	if err != nil {
		return nil, err
	}
	smtpTLS, err := envBool("SMTP_TLS", true)
	if err != nil {
		return nil, err
	}
	pathStyle, err := envBool("S3_USE_PATH_STYLE", true)
	if err != nil {
		return nil, err
	}

	corsOrigins := splitCSV(getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:5173"))
	frontendURL := strings.TrimSpace(os.Getenv("FRONTEND_BASE_URL"))
	if frontendURL == "" && len(corsOrigins) > 0 {
		// The storefront always calls this API cross-origin, so the first
		// allowed origin is the site origin unless it is set explicitly.
		frontendURL = corsOrigins[0]
	}

	cfg := &Config{
		AppEnv:        env,
		AppPort:       getEnv("APP_PORT", "8080"),
		AppBaseURL:    getEnv("APP_BASE_URL", "http://localhost:8080"),
		CredentialKey: strings.TrimSpace(os.Getenv("CREDENTIAL_ENCRYPTION_KEY")),
		Database: DatabaseConfig{
			Host: getEnv("DB_HOST", "localhost"), Port: dbPort, User: getEnv("DB_USER", "postgres"),
			Password: os.Getenv("DB_PASSWORD"), Name: getEnv("DB_NAME", "necly"),
			SSLMode: getEnv("DB_SSL_MODE", "disable"), AllowInsecureTLS: dbAllowInsecure,
			MaxOpenConns: maxOpen, MaxIdleConns: maxIdle,
			ConnMaxLifetime: mustEnvDuration("DB_CONN_MAX_LIFETIME", time.Hour),
			ConnMaxIdleTime: mustEnvDuration("DB_CONN_MAX_IDLE_TIME", 15*time.Minute),
		},
		Redis: RedisConfig{Addr: getEnv("REDIS_ADDR", ""), Password: os.Getenv("REDIS_PASSWORD"), DB: redisDB, Required: redisRequired},
		JWT: JWTConfig{
			Secret: strings.TrimSpace(os.Getenv("JWT_SECRET")), Issuer: getEnv("JWT_ISSUER", "necly"),
			AccessTTL:       mustEnvDuration("JWT_ACCESS_TTL", 15*time.Minute),
			RefreshTTL:      mustEnvDuration("JWT_REFRESH_TTL", 7*24*time.Hour),
			VerificationTTL: mustEnvDuration("JWT_VERIFICATION_TTL", 24*time.Hour),
		},
		RateLimit: RateLimitConfig{RequestsPerMinute: rpm, Burst: burst, LoginPerMinute: loginRPM, RegisterPerHour: registerRPH},
		Payment: PaymentConfig{
			Provider:             strings.ToLower(getEnv("PAYMENT_PROVIDER", "mock")),
			Sandbox:              paymentSandbox,
			MidtransServerKey:    os.Getenv("MIDTRANS_SERVER_KEY"),
			MidtransClientKey:    os.Getenv("MIDTRANS_CLIENT_KEY"),
			MidtransBaseURL:      os.Getenv("MIDTRANS_BASE_URL"),
			MidtransIsProduction: paymentSandbox == false,
			XenditAPIKey:         os.Getenv("XENDIT_API_KEY"),
			XenditCallbackToken:  os.Getenv("XENDIT_CALLBACK_TOKEN"),
			XenditBaseURL:        os.Getenv("XENDIT_BASE_URL"),
			XenditIsProduction:   paymentSandbox == false,
		},
		Email: EmailConfig{
			SMTPHost: os.Getenv("SMTP_HOST"), SMTPPort: smtpPort, SMTPUser: os.Getenv("SMTP_USER"),
			SMTPPassword: os.Getenv("SMTP_PASSWORD"), FromEmail: getEnv("EMAIL_FROM", "noreply@necly.example"),
			FromName: getEnv("EMAIL_FROM_NAME", "Necly Services"), UseTLS: smtpTLS,
		},
		Google: GoogleConfig{
			ClientID: os.Getenv("GOOGLE_CLIENT_ID"), ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
			RedirectURL: os.Getenv("GOOGLE_REDIRECT_URL"), FrontendReturnURL: os.Getenv("GOOGLE_FRONTEND_RETURN_URL"),
			AuthURL:     getEnv("GOOGLE_AUTH_URL", "https://accounts.google.com/o/oauth2/v2/auth"),
			TokenURL:    getEnv("GOOGLE_TOKEN_URL", "https://oauth2.googleapis.com/token"),
			UserInfoURL: getEnv("GOOGLE_USERINFO_URL", "https://openidconnect.googleapis.com/v1/userinfo"),
			StateTTL:    mustEnvDuration("GOOGLE_STATE_TTL", 10*time.Minute),
		},
		Storage: StorageConfig{
			Endpoint: os.Getenv("S3_ENDPOINT"), Region: getEnv("S3_REGION", "us-east-1"), Bucket: os.Getenv("S3_BUCKET"),
			AccessKey: os.Getenv("S3_ACCESS_KEY"), SecretKey: os.Getenv("S3_SECRET_KEY"), PublicBaseURL: os.Getenv("S3_PUBLIC_BASE_URL"),
			UsePathStyle: pathStyle, MaxUploadBytes: maxUpload, PresignTTL: mustEnvDuration("S3_PRESIGN_TTL", 15*time.Minute),
		},
		CORS: CORSConfig{Origins: corsOrigins, AllowCredentials: allowCredentials},
		Cookie: CookieConfig{
			Secure: cookieSecure, HTTPOnly: cookieHTTPOnly, SameSite: getEnv("COOKIE_SAME_SITE", "Lax"),
			Domain: os.Getenv("COOKIE_DOMAIN"), Path: getEnv("COOKIE_PATH", "/api/v1/auth"),
		},
		HTTPClientTimeout: mustEnvDuration("HTTP_CLIENT_TIMEOUT", 10*time.Second),
		AppFrontendURL:    frontendURL,
		PasswordResetTTL:  mustEnvDuration("PASSWORD_RESET_TTL", 45*time.Minute),
		ReportAdminEmail:  strings.TrimSpace(os.Getenv("REPORT_ADMIN_EMAIL")),
	}

	if err := durationLoadError(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

var lastDurationError error

func mustEnvDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		lastDurationError = fmt.Errorf("%s must be a valid duration: %w", key, err)
		return fallback
	}
	return d
}

func durationLoadError() error {
	err := lastDurationError
	lastDurationError = nil
	return err
}

// Validate rejects unsafe or internally inconsistent configuration.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("configuration is required")
	}
	switch c.AppEnv {
	case "development", "test", "production":
	default:
		return errors.New("APP_ENV must be development, test, or production")
	}
	if _, err := strconv.Atoi(c.AppPort); err != nil {
		return errors.New("APP_PORT must be numeric")
	}
	if err := requireHTTPURL("APP_BASE_URL", c.AppBaseURL); err != nil {
		return err
	}
	if c.Database.Host == "" || c.Database.Port <= 0 || c.Database.User == "" || c.Database.Name == "" {
		return errors.New("DB_HOST, DB_PORT, DB_USER, and DB_NAME are required")
	}
	if c.Database.MaxOpenConns <= 0 || c.Database.MaxIdleConns < 0 || c.Database.MaxIdleConns > c.Database.MaxOpenConns {
		return errors.New("database connection pool values are invalid")
	}
	if len(c.JWT.Secret) < 32 || isPlaceholder(c.JWT.Secret) {
		return errors.New("JWT_SECRET must be a non-placeholder value of at least 32 bytes")
	}
	if c.JWT.Issuer == "" || c.JWT.AccessTTL <= 0 || c.JWT.RefreshTTL <= 0 || c.JWT.VerificationTTL <= 0 {
		return errors.New("JWT_ISSUER and positive JWT TTLs are required")
	}
	if c.JWT.AccessTTL > time.Hour || c.JWT.RefreshTTL > 7*24*time.Hour {
		return errors.New("JWT_ACCESS_TTL must not exceed 1h and JWT_REFRESH_TTL must not exceed 168h")
	}
	if c.RateLimit.RequestsPerMinute <= 0 || c.RateLimit.Burst <= 0 || c.RateLimit.LoginPerMinute <= 0 || c.RateLimit.RegisterPerHour <= 0 {
		return errors.New("rate limit values must be positive")
	}
	if c.Cookie.Path == "" || !c.Cookie.HTTPOnly {
		return errors.New("COOKIE_HTTP_ONLY must be true and COOKIE_PATH is required")
	}
	switch strings.ToLower(c.Cookie.SameSite) {
	case "lax", "strict", "none":
	default:
		return errors.New("COOKIE_SAME_SITE must be Lax, Strict, or None")
	}
	if strings.EqualFold(c.Cookie.SameSite, "none") && !c.Cookie.Secure {
		return errors.New("COOKIE_SECURE must be true when COOKIE_SAME_SITE=None")
	}
	if len(c.CORS.Origins) == 0 {
		return errors.New("CORS_ALLOWED_ORIGINS must contain at least one origin")
	}
	for _, origin := range c.CORS.Origins {
		if origin == "*" && c.CORS.AllowCredentials {
			return errors.New("CORS_ALLOWED_ORIGINS cannot contain wildcard when credentials are enabled")
		}
		if origin != "*" {
			if err := requireHTTPURL("CORS_ALLOWED_ORIGINS", origin); err != nil {
				return err
			}
		}
	}
	if c.CredentialKey != "" {
		key, err := base64.StdEncoding.DecodeString(c.CredentialKey)
		if err != nil || len(key) != 32 {
			return errors.New("CREDENTIAL_ENCRYPTION_KEY must be base64 for exactly 32 bytes")
		}
	}
	if err := c.validatePayment(); err != nil {
		return err
	}
	if err := c.validateGoogle(); err != nil {
		return err
	}
	if err := c.validateStorage(); err != nil {
		return err
	}
	if c.HTTPClientTimeout <= 0 {
		return errors.New("HTTP_CLIENT_TIMEOUT must be positive")
	}
	if c.AppEnv == "production" {
		if c.Database.Password == "" || isPlaceholder(c.Database.Password) {
			return errors.New("DB_PASSWORD must be a non-placeholder value in production")
		}
		if (c.Database.SSLMode == "disable" || c.Database.SSLMode == "allow" || c.Database.SSLMode == "prefer") && !c.Database.AllowInsecureTLS {
			return errors.New("DB_SSL_MODE must verify transport in production unless DB_ALLOW_INSECURE_TLS is explicitly true")
		}
		if !c.Cookie.Secure {
			return errors.New("COOKIE_SECURE must be true in production")
		}
		if c.CredentialKey == "" {
			return errors.New("CREDENTIAL_ENCRYPTION_KEY is required in production")
		}
		if c.Payment.Provider == "mock" {
			return errors.New("PAYMENT_PROVIDER mock is not allowed in production")
		}
		if !c.Redis.Required {
			return errors.New("REDIS_REQUIRED must be true in production")
		}
		if !strings.HasPrefix(strings.ToLower(c.AppBaseURL), "https://") {
			return errors.New("APP_BASE_URL must use https in production")
		}
	}
	return nil
}

func (c *Config) validatePayment() error {
	switch c.Payment.Provider {
	case "mock":
		return nil
	case "midtrans":
		if strings.TrimSpace(c.Payment.MidtransServerKey) == "" {
			return errors.New("MIDTRANS_SERVER_KEY is required for PAYMENT_PROVIDER=midtrans")
		}
	case "xendit":
		if strings.TrimSpace(c.Payment.XenditAPIKey) == "" {
			return errors.New("XENDIT_API_KEY is required for PAYMENT_PROVIDER=xendit")
		}
		if strings.TrimSpace(c.Payment.XenditCallbackToken) == "" {
			return errors.New("XENDIT_CALLBACK_TOKEN is required for PAYMENT_PROVIDER=xendit")
		}
	default:
		return errors.New("PAYMENT_PROVIDER must be mock, midtrans, or xendit")
	}
	return nil
}

func (c *Config) validateGoogle() error {
	g := c.Google
	configured := g.ClientID != "" || g.ClientSecret != "" || g.RedirectURL != "" || g.FrontendReturnURL != ""
	if !configured {
		return nil
	}
	if g.ClientID == "" {
		return errors.New("GOOGLE_CLIENT_ID is required when Google OAuth is enabled")
	}
	if g.ClientSecret == "" {
		return errors.New("GOOGLE_CLIENT_SECRET is required when Google OAuth is enabled")
	}
	if err := requireHTTPURL("GOOGLE_REDIRECT_URL", g.RedirectURL); err != nil {
		return err
	}
	if err := requireHTTPURL("GOOGLE_FRONTEND_RETURN_URL", g.FrontendReturnURL); err != nil {
		return err
	}
	if g.StateTTL <= 0 {
		return errors.New("GOOGLE_STATE_TTL must be positive")
	}
	return nil
}

func (c *Config) validateStorage() error {
	s := c.Storage
	configured := s.Endpoint != "" || s.Bucket != "" || s.AccessKey != "" || s.SecretKey != "" || s.PublicBaseURL != ""
	if !configured {
		return nil
	}
	if s.Endpoint == "" {
		return errors.New("S3_ENDPOINT is required when object storage is enabled")
	}
	if s.Bucket == "" {
		return errors.New("S3_BUCKET is required when object storage is enabled")
	}
	if s.Region == "" {
		return errors.New("S3_REGION is required when object storage is enabled")
	}
	if (s.AccessKey == "") != (s.SecretKey == "") {
		return errors.New("S3_ACCESS_KEY and S3_SECRET_KEY must be configured together")
	}
	if s.MaxUploadBytes <= 0 || s.PresignTTL <= 0 {
		return errors.New("S3_MAX_UPLOAD_BYTES and S3_PRESIGN_TTL must be positive")
	}
	return nil
}

func requireHTTPURL(name, value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%s must be an absolute http(s) URL", name)
	}
	return nil
}

func isPlaceholder(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	return v == "postgres" || v == "password" || v == "secret" || strings.Contains(v, "change-me") || strings.Contains(v, "changeme") || strings.Contains(v, "placeholder")
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return i, nil
}

func envInt64(key string, fallback int64) (int64, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	i, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return i, nil
}

func envBool(key string, fallback bool) (bool, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return parsed, nil
}
