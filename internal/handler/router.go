package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
	"github.com/redis/go-redis/v9"
)

func parseBoolPtr(s string) *bool {
	if s == "" {
		return nil
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return nil
	}
	return &b
}

type Router struct {
	authSvc           *service.AuthService
	catalogSvc        *service.CatalogService
	cartSvc           *service.CartService
	checkoutSvc       *service.CheckoutService
	webhookSvc        *service.WebhookService
	orderSvc          *service.OrderService
	adminSvc          *service.AdminDashboardService
	adminUserSvc      *service.AdminUserService
	adminProductSvc   *service.AdminProductService
	adminCategorySvc  *service.AdminCategoryService
	adminOrderSvc     *service.AdminOrderService
	adminVoucherSvc   *service.AdminVoucherService
	adminInventorySvc *service.AdminInventoryService
	reportSvc         *service.ReportService
	testimonialSvc    *service.TestimonialService
	tokens            *service.TokenManager
	cipher            *service.CredentialCipher
	redis             *redis.Client
}

func NewRouter(authSvc *service.AuthService, catalogSvc *service.CatalogService, cartSvc *service.CartService, checkoutSvc *service.CheckoutService, webhookSvc *service.WebhookService, orderSvc *service.OrderService, adminSvc *service.AdminDashboardService, adminUserSvc *service.AdminUserService, adminProductSvc *service.AdminProductService, adminCategorySvc *service.AdminCategoryService, adminOrderSvc *service.AdminOrderService, adminVoucherSvc *service.AdminVoucherService, adminInventorySvc *service.AdminInventoryService, tokens *service.TokenManager, cipher *service.CredentialCipher, redis *redis.Client, reportSvc *service.ReportService, testimonialSvc *service.TestimonialService) *Router {
	return &Router{authSvc: authSvc, catalogSvc: catalogSvc, cartSvc: cartSvc, checkoutSvc: checkoutSvc, webhookSvc: webhookSvc, orderSvc: orderSvc, adminSvc: adminSvc, adminUserSvc: adminUserSvc, adminProductSvc: adminProductSvc, adminCategorySvc: adminCategorySvc, adminOrderSvc: adminOrderSvc, adminVoucherSvc: adminVoucherSvc, adminInventorySvc: adminInventorySvc, reportSvc: reportSvc, testimonialSvc: testimonialSvc, tokens: tokens, cipher: cipher, redis: redis}
}

func (r *Router) Register(app *fiber.App) {
	api := app.Group("/api/v1")

	r.registerHealth(app)
	r.registerPublic(api)
	r.registerAuth(api)
	r.registerProtected(api)
	r.registerAdmin(api)
}

func (r *Router) registerHealth(app *fiber.App) {
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "service": "necly-backend"})
	})
	app.Get("/ready", func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
		defer cancel()

		// Check database
		if err := r.adminSvc.PingDatabase(ctx); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"status": "not ready",
				"reason": "database unavailable",
				"error":  err.Error(),
			})
		}

		// Check Redis if required
		if r.redis != nil {
			if err := r.redis.Ping(ctx).Err(); err != nil {
				return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
					"status": "not ready",
					"reason": "redis unavailable",
					"error":  err.Error(),
				})
			}
		}

		return c.JSON(fiber.Map{"status": "ready"})
	})
}

func (r *Router) registerPublic(api fiber.Router) {
	// Payment providers cannot send an application access token. Keep the
	// canonical webhook endpoint outside the authenticated catch-all group;
	// authenticity is verified by each provider's signature/token instead.
	api.Post("/webhooks/:provider", r.receiveWebhook)

	public := api.Group("/public")
	public.Get("/categories", r.listCategories)
	public.Get("/categories/:id", r.getCategory)
	public.Get("/products", r.listProducts)
	public.Get("/products/:id", r.getProduct)
	public.Get("/products/:id/prices", r.listProductPrices)
	// QA baris 55-57: "Laporan Kendala" is filed by visitors who are not signed
	// in yet, so it sits with the other public routes.
	public.Post("/reports", r.createReport)
	// QA poin 16: halaman "Testimoni" reads the approved reviews. Public because
	// the page is reachable without signing in.
	public.Get("/testimonials", r.listTestimonials)
	// Backward-compatible alias used by early development deployments.
	public.Post("/webhooks/:provider", r.receiveWebhook)
}

func (r *Router) registerAuth(api fiber.Router) {
	auth := api.Group("/auth")
	auth.Post("/register", r.register)
	auth.Post("/login", r.login)
	auth.Post("/refresh", r.refresh)
	auth.Post("/logout", r.logout)
	auth.Post("/verify-email", r.verifyEmail)
	// A registration email that never arrives (spam folder, typo, closed tab)
	// used to strand the account: login refuses unverified users and there was no
	// way to request a new link.
	auth.Post("/resend-verification", r.resendVerification)
	// QA baris 60: "Lupa kata sandi" needs a real flow, not a dead button.
	auth.Post("/forgot-password", r.forgotPassword)
	auth.Post("/reset-password", r.resetPassword)
	auth.Get("/google", r.googleLogin)
	auth.Get("/google/callback", r.googleCallback)
}

func (r *Router) registerProtected(api fiber.Router) {
	protected := api.Group("", r.authMiddleware())
	protected.Get("/me", r.getMe)
	protected.Post("/cart/items", r.addToCart)
	protected.Get("/cart", r.getCart)
	protected.Patch("/cart/items/:itemId", r.updateCartItem)
	protected.Delete("/cart/items/:itemId", r.removeCartItem)
	protected.Post("/checkout", r.checkout)
	protected.Get("/orders", r.listOrders)
	protected.Get("/orders/:id", r.getOrder)
	// Account security: these need the access token and, for the session checks,
	// the refresh cookie that identifies the device making the request.
	protected.Post("/auth/change-password", r.changePassword)
	protected.Get("/auth/sessions", r.listSessions)
	protected.Delete("/auth/sessions", r.revokeOtherSessions)
	protected.Delete("/auth/sessions/:id", r.revokeSession)
}

func (r *Router) registerAdmin(api fiber.Router) {
	admin := api.Group("/admin", r.authMiddleware(), r.requireRole(domain.RoleAdmin))
	admin.Get("/dashboard", r.dashboard)
	admin.Get("/users", r.listUsers)
	admin.Get("/users/:id", r.getUser)
	admin.Patch("/users/:id", r.updateUser)
	admin.Delete("/users/:id", r.deleteUser)
	admin.Get("/products", r.adminListProducts)
	admin.Post("/products", r.adminCreateProduct)
	admin.Patch("/products/:id", r.adminUpdateProduct)
	admin.Delete("/products/:id", r.adminDeleteProduct)
	admin.Get("/categories", r.adminListCategories)
	admin.Post("/categories", r.adminCreateCategory)
	admin.Patch("/categories/:id", r.adminUpdateCategory)
	admin.Delete("/categories/:id", r.adminDeleteCategory)
	admin.Get("/orders", r.adminListOrders)
	admin.Patch("/orders/:id/status", r.adminUpdateOrderStatus)
	admin.Get("/audit-logs", r.adminListAuditLogs)
	// Voucher management
	admin.Get("/vouchers", r.adminListVouchers)
	admin.Post("/vouchers", r.adminCreateVoucher)
	admin.Get("/vouchers/:id", r.adminGetVoucher)
	admin.Patch("/vouchers/:id", r.adminUpdateVoucher)
	admin.Delete("/vouchers/:id", r.adminDeleteVoucher)
	// Inventory account management
	admin.Get("/inventory", r.adminListInventoryAccounts)
	admin.Post("/inventory", r.adminCreateInventoryAccount)
	admin.Get("/inventory/:id", r.adminGetInventoryAccount)
	admin.Patch("/inventory/:id", r.adminUpdateInventoryAccount)
	admin.Delete("/inventory/:id", r.adminDeleteInventoryAccount)
	// Payment list for dashboard
	admin.Get("/payments", r.adminListPayments)
	// Notification list for dashboard
	admin.Get("/notifications", r.adminListNotifications)
}

func (r *Router) authMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Header otorisasi tidak ditemukan"})
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Format otorisasi tidak valid"})
		}

		claims, err := r.tokens.ParseAccess(parts[1])
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Token tidak valid atau telah kedaluwarsa"})
		}

		c.Locals("user_id", claims.Subject)
		c.Locals("user_role", claims.Role)
		return c.Next()
	}
}

func (r *Router) requireRole(requiredRole domain.UserRole) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// The claim is stored in Locals as a plain string. Comparing that `any`
		// against a domain.UserRole compares the dynamic types, which never match,
		// so every /admin route answered 403 — including for genuine admins.
		role, _ := c.Locals("user_role").(string)
		if domain.UserRole(role) != requiredRole {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Akses ditolak: Anda tidak memiliki izin untuk tindakan ini"})
		}
		return c.Next()
	}
}

func (r *Router) register(c *fiber.Ctx) error {
	var input service.RegisterInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	result, err := r.authSvc.Register(ctx, input)
	if err != nil {
		// Matching on the English substrings "conflict"/"invalid" never worked:
		// both sentinels carry Indonesian messages, so a taken address and a short
		// password both answered 500.
		switch {
		case errors.Is(err, service.ErrConflict):
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Email sudah terdaftar"})
		case errors.Is(err, service.ErrInvalidInput):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		log.Printf("register failed for %q: %v", input.Email, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Pendaftaran akun gagal"})
	}

	return c.Status(fiber.StatusCreated).JSON(result)
}

func (r *Router) login(c *fiber.Ctx) error {
	var input service.LoginInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	session, err := r.authSvc.Login(ctx, input, service.ClientInfo{
		IPAddress: c.IP(),
		UserAgent: c.Get("User-Agent"),
	})
	if err != nil {
		if err == service.ErrUnauthorized || err == service.ErrEmailNotVerified {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal masuk ke akun"})
	}

	setRefreshCookie(c, session.RefreshToken, session.RefreshExpiresAt)

	return c.JSON(fiber.Map{
		"user":         session.User,
		"access_token": session.AccessToken,
		"expires_in":   session.ExpiresIn,
	})
}

func (r *Router) refresh(c *fiber.Ctx) error {
	refreshToken := c.Cookies("refresh_token")
	if refreshToken == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Token penyegar tidak ditemukan"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	session, err := r.authSvc.Refresh(ctx, refreshToken, service.ClientInfo{
		IPAddress: c.IP(),
		UserAgent: c.Get("User-Agent"),
	})
	if err != nil {
		clearRefreshCookie(c)
		if err == service.ErrUnauthorized {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Sesi telah kedaluwarsa atau dicabut, silakan masuk kembali"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal memperbarui sesi"})
	}

	setRefreshCookie(c, session.RefreshToken, session.RefreshExpiresAt)

	return c.JSON(fiber.Map{
		"user":         session.User,
		"access_token": session.AccessToken,
		"expires_in":   session.ExpiresIn,
	})
}

// setRefreshCookie writes the session cookie. The lifetime comes from the
// configured refresh TTL, so raising JWT_REFRESH_TTL no longer leaves the cookie
// expiring at a hardcoded week.
func setRefreshCookie(c *fiber.Ctx, token string, expiresAt time.Time) {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge <= 0 {
		maxAge = int((7 * 24 * time.Hour).Seconds())
	}
	c.Cookie(&fiber.Cookie{
		Name:     "refresh_token",
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HTTPOnly: true,
		Secure:   c.Protocol() == "https",
		SameSite: "Lax",
	})
}

// clearRefreshCookie expires the session cookie. ClearCookie() emits no Path, so
// the browser scoped the deletion to the request path (..auth) and the real
// cookie — written with Path=/ — survived a logout.
func clearRefreshCookie(c *fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Now().Add(-time.Hour),
		HTTPOnly: true,
		Secure:   c.Protocol() == "https",
		SameSite: "Lax",
	})
}

func (r *Router) logout(c *fiber.Ctx) error {
	refreshToken := c.Cookies("refresh_token")
	if refreshToken != "" {
		ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
		defer cancel()
		// A failed revoke leaves a usable token behind, so the operator has to be
		// able to see it in the log rather than losing it silently.
		if err := r.authSvc.Logout(ctx, refreshToken); err != nil {
			log.Printf("logout: revoking refresh token failed: %v", err)
		}
	}
	clearRefreshCookie(c)
	return c.JSON(fiber.Map{"message": "Berhasil keluar"})
}

func (r *Router) changePassword(c *fiber.Ctx) error {
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}
	userID, _ := c.Locals("user_id").(string)

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	err := r.authSvc.ChangePassword(ctx, userID, input.CurrentPassword, input.NewPassword, c.Cookies("refresh_token"), service.ClientInfo{
		IPAddress: c.IP(),
		UserAgent: c.Get("User-Agent"),
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidInput):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		case errors.Is(err, service.ErrNotFound):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Akun tidak ditemukan"})
		}
		log.Printf("change-password failed for %q: %v", userID, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Kata sandi gagal diubah"})
	}
	return c.JSON(fiber.Map{"message": "Kata sandi berhasil diubah. Sesi lain sudah diakhiri."})
}

func (r *Router) listSessions(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	sessions, err := r.authSvc.ListSessions(ctx, userID, c.Cookies("refresh_token"))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil daftar sesi"})
	}
	return c.JSON(fiber.Map{"sessions": sessions})
}

func (r *Router) revokeSession(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	err := r.authSvc.RevokeSession(ctx, userID, c.Params("id"), c.Cookies("refresh_token"), service.ClientInfo{
		IPAddress: c.IP(),
		UserAgent: c.Get("User-Agent"),
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidInput):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		case errors.Is(err, service.ErrNotFound):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Sesi tidak ditemukan atau sudah berakhir"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Sesi gagal diakhiri"})
	}
	return c.JSON(fiber.Map{"message": "Sesi diakhiri"})
}

func (r *Router) revokeOtherSessions(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	ended, err := r.authSvc.RevokeOtherSessions(ctx, userID, c.Cookies("refresh_token"), service.ClientInfo{
		IPAddress: c.IP(),
		UserAgent: c.Get("User-Agent"),
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Sesi lain gagal diakhiri"})
	}
	return c.JSON(fiber.Map{"message": "Sesi lain sudah diakhiri", "ended": ended})
}

func (r *Router) verifyEmail(c *fiber.Ctx) error {
	var input struct {
		Token string `json:"token"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	user, err := r.authSvc.VerifyEmail(ctx, input.Token)
	if err != nil {
		if err == service.ErrUnauthorized || err == service.ErrNotFound {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Token verifikasi tidak valid atau telah kedaluwarsa"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Verifikasi email gagal"})
	}

	return c.JSON(fiber.Map{"message": "Email berhasil diverifikasi", "user": user})
}

func (r *Router) resendVerification(c *fiber.Ctx) error {
	var input struct {
		Email string `json:"email"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	if err := r.authSvc.ResendVerification(ctx, input.Email); err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidInput):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Masukkan alamat email yang valid."})
		case errors.Is(err, service.ErrNotFound):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Email belum terdaftar. Silakan membuat akun terlebih dahulu."})
		case errors.Is(err, service.ErrAlreadyVerified):
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
		}
		log.Printf("resend-verification failed for %q: %v", input.Email, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengirim ulang tautan verifikasi."})
	}

	return c.JSON(fiber.Map{"message": "Tautan verifikasi baru sudah dikirim ke email Anda."})
}

func (r *Router) forgotPassword(c *fiber.Ctx) error {
	var input struct {
		Email string `json:"email"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()

	if err := r.authSvc.ForgotPassword(ctx, input.Email); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Email belum terdaftar. Silakan membuat akun terlebih dahulu.",
			})
		}
		if errors.Is(err, service.ErrInvalidInput) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Masukkan alamat email yang valid."})
		}
		log.Printf("forgot-password failed for %q: %v", input.Email, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Permintaan atur ulang kata sandi gagal."})
	}

	return c.JSON(fiber.Map{"message": "Tautan atur ulang kata sandi sudah dikirim."})
}

func (r *Router) resetPassword(c *fiber.Ctx) error {
	var input struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	user, err := r.authSvc.ResetPassword(ctx, input.Token, input.Password)
	if err != nil {
		if errors.Is(err, service.ErrUnauthorized) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Tautan atur ulang kata sandi tidak valid atau sudah kedaluwarsa. Silakan minta tautan baru."})
		}
		if errors.Is(err, service.ErrInvalidInput) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengatur ulang kata sandi"})
	}

	return c.JSON(fiber.Map{"message": "Kata sandi berhasil diperbarui. Silakan masuk dengan kata sandi baru Anda.", "user": user})
}

// createReport accepts a "Laporan Kendala" submission (QA baris 55-57).
func (r *Router) createReport(c *fiber.Ctx) error {
	var input service.ReportInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()

	report, err := r.reportSvc.Create(ctx, input, clientIP(c))
	if err != nil {
		var validation *service.ValidationError
		var cooldown *service.CooldownError
		switch {
		case errors.As(err, &validation):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": validation.Error(), "fields": validation.Fields})
		case errors.As(err, &cooldown):
			retryAfter := int(cooldown.RetryAfter.Seconds()) + 1
			c.Set("Retry-After", strconv.Itoa(retryAfter))
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error":       cooldown.Error(),
				"retry_after": retryAfter,
			})
		case errors.Is(err, service.ErrReportMismatch):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengirim laporan kendala"})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"message": "Laporan kendala diterima", "report": report})
}

// clientIP resolves the visitor's address. The API runs behind a Cloudflare
// tunnel, so the socket peer is always the tunnel and never the visitor; the
// forwarded headers are the only source of the real address. The value is
// stored for admin review only and never used for an authorization decision.
func clientIP(c *fiber.Ctx) string {
	if ip := strings.TrimSpace(c.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	if forwarded := c.Get("X-Forwarded-For"); forwarded != "" {
		if ip := strings.TrimSpace(strings.Split(forwarded, ",")[0]); ip != "" {
			return ip
		}
	}
	return c.IP()
}

func (r *Router) googleLogin(c *fiber.Ctx) error {
	if r.authSvc.GoogleConfig() == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Autentikasi Google belum dikonfigurasi"})
	}
	cfg := r.authSvc.GoogleConfig()
	state, err := r.authSvc.GenerateOAuthState()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal membuat state autentikasi Google"})
	}
	// Store state in Redis with TTL
	if r.redis != nil {
		ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
		defer cancel()
		if err := r.redis.Set(ctx, "oauth:state:"+state, c.IP(), cfg.StateTTL).Err(); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal menyimpan state autentikasi Google"})
		}
	}
	authURL := fmt.Sprintf("%s?client_id=%s&redirect_uri=%s&response_type=code&scope=openid%%20email%%20profile&access_type=offline&prompt=consent&state=%s",
		cfg.AuthURL, url.QueryEscape(cfg.ClientID), url.QueryEscape(cfg.RedirectURL), state)
	return c.Redirect(authURL, fiber.StatusTemporaryRedirect)
}

func (r *Router) googleCallback(c *fiber.Ctx) error {
	if r.authSvc.GoogleConfig() == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Autentikasi Google belum dikonfigurasi"})
	}
	cfg := r.authSvc.GoogleConfig()
	code := c.Query("code")
	state := c.Query("state")
	if code == "" || state == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Kode otorisasi atau state Google tidak lengkap"})
	}
	// Verify state
	if r.redis != nil {
		ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
		defer cancel()
		storedIP, err := r.redis.Get(ctx, "oauth:state:"+state).Result()
		if err != nil || storedIP != c.IP() {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Sesi login Google tidak valid atau kedaluwarsa"})
		}
		r.redis.Del(ctx, "oauth:state:"+state)
	}
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()
	session, err := r.authSvc.ExchangeGoogleCode(ctx, code, cfg)
	if err != nil {
		if err == service.ErrUnauthorized {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Kode otorisasi Google tidak valid"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal menghubungkan akun Google"})
	}
	// The session cookie is set here and the tokens stay out of the redirect: a
	// query string ends up in browser history, the Referer header, and every
	// proxy log along the way. The storefront recovers the session on load by
	// calling /auth/refresh, which sends this cookie.
	setRefreshCookie(c, session.RefreshToken, session.RefreshExpiresAt)
	return c.Redirect(cfg.FrontendReturnURL, fiber.StatusTemporaryRedirect)
}

func (r *Router) listCategories(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	defer cancel()
	categories, _, err := r.catalogSvc.ListCategories(ctx, true, 1, 100)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil daftar kategori"})
	}
	return c.JSON(fiber.Map{"categories": categories})
}

func (r *Router) getCategory(c *fiber.Ctx) error {
	id := c.Params("id")
	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	defer cancel()
	cat, err := r.catalogSvc.GetCategory(ctx, id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Kategori tidak ditemukan"})
	}
	return c.JSON(cat)
}

func (r *Router) listProducts(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	defer cancel()
	categoryID := c.Query("category")
	search := c.Query("search")
	page := c.QueryInt("page", 1)
	perPage := c.QueryInt("per_page", 20)
	if perPage > 100 {
		perPage = 100
	}
	products, total, err := r.catalogSvc.ListProductsWithStock(ctx, categoryID, search, true, page, perPage)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil daftar produk"})
	}
	return c.JSON(fiber.Map{"products": products, "page": page, "per_page": perPage, "total": total})
}

func (r *Router) getProduct(c *fiber.Ctx) error {
	id := c.Params("id")
	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	defer cancel()

	// First try as UUID, then as slug
	product, err := r.catalogSvc.GetProductWithStock(ctx, id)
	if err != nil {
		// Try slug lookup if ID lookup failed
		product, err = r.catalogSvc.GetProductBySlugWithStock(ctx, id)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Produk langganan tidak ditemukan"})
		}
	}
	return c.JSON(product)
}

func (r *Router) listProductPrices(c *fiber.Ctx) error {
	id := c.Params("id")
	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	defer cancel()
	prices, err := r.catalogSvc.ListProductPrices(ctx, id, true)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil daftar harga paket"})
	}
	return c.JSON(fiber.Map{"prices": prices})
}

func (r *Router) listTestimonials(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	defer cancel()
	page := c.QueryInt("page", 1)
	perPage := c.QueryInt("per_page", 20)
	if perPage > 100 {
		perPage = 100
	}
	testimonials, total, err := r.testimonialSvc.ListPublic(ctx, page, perPage)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil testimoni"})
	}
	return c.JSON(fiber.Map{"testimonials": testimonials, "page": page, "per_page": perPage, "total": total})
}

func (r *Router) getMe(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	defer cancel()
	user, err := r.authSvc.GetUserByID(ctx, userID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Pengguna tidak ditemukan"})
	}
	return c.JSON(user)
}

func (r *Router) addToCart(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var input struct {
		ProductPriceID string `json:"product_price_id"`
		Quantity       int    `json:"quantity"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}
	input.ProductPriceID = strings.TrimSpace(input.ProductPriceID)
	if input.ProductPriceID == "" || input.Quantity <= 0 || input.Quantity > 100 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Pilihan paket langganan dan jumlah slot (1-100) wajib diisi"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	item, err := r.cartSvc.AddItem(ctx, userID, input.ProductPriceID, input.Quantity)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Paket langganan tidak ditemukan"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal menambahkan ke keranjang"})
	}

	return c.Status(fiber.StatusCreated).JSON(item)
}

func (r *Router) getCart(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	cart, err := r.cartSvc.GetCartWithValidation(ctx, userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil data keranjang"})
	}

	var total int64
	for _, item := range cart.Items {
		if item.ProductPrice != nil {
			total += item.ProductPrice.Amount * int64(item.Quantity)
		}
	}

	return c.JSON(fiber.Map{"items": cart.Items, "total": total})
}

func (r *Router) updateCartItem(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	itemID := c.Params("itemId")

	var input struct {
		Quantity int `json:"quantity"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}
	if input.Quantity <= 0 || input.Quantity > 100 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Jumlah slot harus antara 1 dan 100"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	err := r.cartSvc.UpdateItem(ctx, userID, itemID, input.Quantity)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Item keranjang tidak ditemukan"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal memperbarui item keranjang"})
	}

	return c.JSON(fiber.Map{"message": "Item keranjang berhasil diperbarui"})
}

func (r *Router) removeCartItem(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	itemID := c.Params("itemId")

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	err := r.cartSvc.RemoveItem(ctx, userID, itemID)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Item keranjang tidak ditemukan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal menghapus item dari keranjang"})
	}

	return c.JSON(fiber.Map{"message": "Item keranjang berhasil dihapus"})
}

func (r *Router) checkout(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var input service.CheckoutInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}
	input.UserID = userID

	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()

	result, err := r.checkoutSvc.Checkout(ctx, input)
	if err != nil {
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Produk atau voucher tidak ditemukan"})
		}
		if err == service.ErrOutOfStock {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Slot akun langganan tidak mencukupi atau sedang habis"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Proses pemesanan gagal"})
	}

	return c.Status(fiber.StatusCreated).JSON(result)
}

func (r *Router) listOrders(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	page := c.QueryInt("page", 1)
	perPage := c.QueryInt("per_page", 20)
	if perPage > 100 {
		perPage = 100
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	orders, total, err := r.orderSvc.ListOrders(ctx, userID, page, perPage)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil riwayat pesanan"})
	}

	return c.JSON(fiber.Map{"orders": orders, "page": page, "per_page": perPage, "total": total})
}

func (r *Router) getOrder(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	id := c.Params("id")

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	order, err := r.orderSvc.GetOrder(ctx, userID, id)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Pesanan tidak ditemukan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil detail pesanan"})
	}

	return c.JSON(order)
}

func (r *Router) dashboard(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()

	stats, err := r.adminSvc.GetDashboardStats(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil statistik dashboard"})
	}

	return c.JSON(stats)
}

func (r *Router) listUsers(c *fiber.Ctx) error {
	filter := service.UserFilter{
		Role:   c.Query("role"),
		Active: parseBoolPtr(c.Query("active")),
		Search: c.Query("search"),
	}
	page := c.QueryInt("page", 1)
	perPage := c.QueryInt("per_page", 20)
	if perPage > 100 {
		perPage = 100
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	users, total, err := r.adminUserSvc.ListUsers(ctx, filter, page, perPage)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil daftar pengguna"})
	}

	// Clear password hashes from response
	for i := range users {
		users[i].PasswordHash = ""
	}

	return c.JSON(fiber.Map{"users": users, "page": page, "per_page": perPage, "total": total})
}

func (r *Router) getUser(c *fiber.Ctx) error {
	id := c.Params("id")

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	user, err := r.adminUserSvc.GetUser(ctx, id)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Pengguna tidak ditemukan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil data pengguna"})
	}

	user.PasswordHash = ""
	return c.JSON(user)
}

func (r *Router) updateUser(c *fiber.Ctx) error {
	id := c.Params("id")

	var input struct {
		Name   string `json:"name"`
		Email  string `json:"email"`
		Active *bool  `json:"active"`
		Role   string `json:"role"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	var role *domain.UserRole
	if input.Role != "" {
		r := domain.UserRole(input.Role)
		role = &r
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	user, err := r.adminUserSvc.UpdateUser(ctx, id, input.Name, input.Email, input.Active)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Pengguna tidak ditemukan"})
		}
		if err == service.ErrConflict {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Email sudah digunakan oleh akun lain"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal memperbarui data pengguna"})
	}

	if role != nil {
		user, err = r.adminUserSvc.UpdateUserRole(ctx, id, *role)
		if err != nil {
			if err == service.ErrInvalidInput {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Peran pengguna tidak valid"})
			}
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal memperbarui peran pengguna"})
		}
	}

	user.PasswordHash = ""
	return c.JSON(user)
}

func (r *Router) deleteUser(c *fiber.Ctx) error {
	id := c.Params("id")

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	err := r.adminUserSvc.DeleteUser(ctx, id)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Pengguna tidak ditemukan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal menghapus pengguna"})
	}

	return c.JSON(fiber.Map{"message": "Pengguna berhasil dihapus"})
}

func (r *Router) adminListProducts(c *fiber.Ctx) error {
	filter := service.AdminProductFilter{
		CategoryID: c.Query("category_id"),
		Active:     parseBoolPtr(c.Query("active")),
		Search:     c.Query("search"),
	}
	page := c.QueryInt("page", 1)
	perPage := c.QueryInt("per_page", 20)
	if perPage > 100 {
		perPage = 100
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	products, total, err := r.adminProductSvc.ListProducts(ctx, filter, page, perPage)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil daftar produk"})
	}

	return c.JSON(fiber.Map{"products": products, "page": page, "per_page": perPage, "total": total})
}

func (r *Router) adminCreateProduct(c *fiber.Ctx) error {
	var input struct {
		CategoryID  string `json:"category_id"`
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Description string `json:"description"`
		ImageURL    string `json:"image_url"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	product, err := r.adminProductSvc.CreateProduct(ctx, input.CategoryID, input.Name, input.Slug, input.Description, input.ImageURL)
	if err != nil {
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if err == service.ErrConflict {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "SKU/Slug produk sudah digunakan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal menambahkan produk"})
	}

	return c.Status(fiber.StatusCreated).JSON(product)
}

func (r *Router) adminUpdateProduct(c *fiber.Ctx) error {
	id := c.Params("id")

	var input struct {
		CategoryID  string `json:"category_id"`
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Description string `json:"description"`
		ImageURL    string `json:"image_url"`
		Active      *bool  `json:"active"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	product, err := r.adminProductSvc.UpdateProduct(ctx, id, input.CategoryID, input.Name, input.Slug, input.Description, input.ImageURL, input.Active)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Produk langganan tidak ditemukan"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if err == service.ErrConflict {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "SKU/Slug produk sudah digunakan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal memperbarui data produk"})
	}

	return c.JSON(product)
}

func (r *Router) adminDeleteProduct(c *fiber.Ctx) error {
	id := c.Params("id")

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	err := r.adminProductSvc.DeleteProduct(ctx, id)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Produk langganan tidak ditemukan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal menghapus produk"})
	}

	return c.JSON(fiber.Map{"message": "Produk berhasil dihapus"})
}

func (r *Router) adminListCategories(c *fiber.Ctx) error {
	filter := service.CategoryFilter{
		Active: parseBoolPtr(c.Query("active")),
		Search: c.Query("search"),
	}
	page := c.QueryInt("page", 1)
	perPage := c.QueryInt("per_page", 20)
	if perPage > 100 {
		perPage = 100
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	categories, total, err := r.adminCategorySvc.ListCategories(ctx, filter, page, perPage)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil daftar kategori"})
	}

	return c.JSON(fiber.Map{"categories": categories, "page": page, "per_page": perPage, "total": total})
}

func (r *Router) adminCreateCategory(c *fiber.Ctx) error {
	var input struct {
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Description string `json:"description"`
		ImageURL    string `json:"image_url"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	category, err := r.adminCategorySvc.CreateCategory(ctx, input.Name, input.Slug, input.Description, input.ImageURL)
	if err != nil {
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if err == service.ErrConflict {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Slug kategori sudah digunakan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal membuat kategori"})
	}

	return c.Status(fiber.StatusCreated).JSON(category)
}

func (r *Router) adminUpdateCategory(c *fiber.Ctx) error {
	id := c.Params("id")

	var input struct {
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Description string `json:"description"`
		ImageURL    string `json:"image_url"`
		Active      *bool  `json:"active"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	category, err := r.adminCategorySvc.UpdateCategory(ctx, id, input.Name, input.Slug, input.Description, input.ImageURL, input.Active)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Kategori tidak ditemukan"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if err == service.ErrConflict {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Slug kategori sudah digunakan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal memperbarui kategori"})
	}

	return c.JSON(category)
}

func (r *Router) adminDeleteCategory(c *fiber.Ctx) error {
	id := c.Params("id")

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	err := r.adminCategorySvc.DeleteCategory(ctx, id)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Kategori tidak ditemukan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal menghapus kategori"})
	}

	return c.JSON(fiber.Map{"message": "Kategori berhasil dihapus"})
}

func (r *Router) adminListOrders(c *fiber.Ctx) error {
	filter := service.OrderFilter{
		UserID:   c.Query("user_id"),
		Status:   c.Query("status"),
		Search:   c.Query("search"),
		DateFrom: nil,
		DateTo:   nil,
	}
	page := c.QueryInt("page", 1)
	perPage := c.QueryInt("per_page", 20)
	if perPage > 100 {
		perPage = 100
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	orders, total, err := r.adminOrderSvc.ListOrders(ctx, filter, page, perPage)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil riwayat pesanan"})
	}

	return c.JSON(fiber.Map{"orders": orders, "page": page, "per_page": perPage, "total": total})
}

func (r *Router) adminUpdateOrderStatus(c *fiber.Ctx) error {
	id := c.Params("id")

	var input struct {
		Status string `json:"status"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}
	if input.Status == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Status pesanan wajib diisi"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	order, err := r.adminOrderSvc.UpdateOrderStatus(ctx, id, domain.OrderStatus(input.Status))
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Pesanan tidak ditemukan"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal memperbarui status pesanan"})
	}

	return c.JSON(order)
}

func (r *Router) receiveWebhook(c *fiber.Ctx) error {
	provider := c.Params("provider")
	if provider == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Penyedia pembayaran wajib diisi"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
	defer cancel()

	payload := c.Body()
	headers := make(map[string]string)
	c.Request().Header.VisitAll(func(key, value []byte) {
		headers[string(key)] = string(value)
	})
	// Route the callback to the provider named in the URL instead of trying all
	// registered providers in map order.
	headers["X-Provider"] = strings.ToLower(strings.TrimSpace(provider))

	result, err := r.webhookSvc.Process(ctx, headers, payload)
	if err != nil {
		if errors.Is(err, service.ErrUnauthorized) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Tanda tangan webhook tidak valid"})
		}
		if errors.Is(err, service.ErrInvalidInput) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if errors.Is(err, service.ErrNotFound) {
			// A notification for a payment session we hold no row for. Answering
			// 2xx stops the provider from retrying something we can never apply,
			// while the log keeps it visible to the operator.
			log.Printf("webhook %s: unknown payment reference in callback", provider)
			return c.JSON(fiber.Map{"processed": false, "reason": "unknown_payment"})
		}
		log.Printf("webhook %s: processing failed: %v", provider, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Pemrosesan webhook gagal"})
	}
	// Leaves an unambiguous trace that a callback actually changed an order, as
	// opposed to being a duplicate we only acknowledged.
	if result.Processed {
		log.Printf("webhook %s: payment event applied to order", provider)
	}

	return c.JSON(result)
}

func (r *Router) adminListAuditLogs(c *fiber.Ctx) error {
	filter := service.AuditLogFilter{
		Action:   c.Query("action"),
		Entity:   c.Query("entity"),
		EntityID: c.Query("entity_id"),
		UserID:   c.Query("user_id"),
		FromDate: c.Query("from_date"),
		ToDate:   c.Query("to_date"),
	}
	page := c.QueryInt("page", 1)
	perPage := c.QueryInt("per_page", 50)
	if perPage > 200 {
		perPage = 200
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	logs, total, err := r.adminSvc.ListAuditLogs(ctx, filter, page, perPage)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil log audit"})
	}

	return c.JSON(fiber.Map{"logs": logs, "page": page, "per_page": perPage, "total": total})
}

func (r *Router) adminListVouchers(c *fiber.Ctx) error {
	filter := service.VoucherFilter{
		Active:   parseBoolPtr(c.Query("active")),
		Code:     c.Query("code"),
		DateFrom: nil,
		DateTo:   nil,
	}
	if from := c.Query("from_date"); from != "" {
		if t, err := time.Parse(time.RFC3339, from); err == nil {
			filter.DateFrom = &t
		}
	}
	if to := c.Query("to_date"); to != "" {
		if t, err := time.Parse(time.RFC3339, to); err == nil {
			filter.DateTo = &t
		}
	}
	page := c.QueryInt("page", 1)
	perPage := c.QueryInt("per_page", 20)
	if perPage > 100 {
		perPage = 100
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	vouchers, total, err := r.adminVoucherSvc.ListVouchers(ctx, filter, page, perPage)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil daftar voucher"})
	}

	return c.JSON(fiber.Map{"vouchers": vouchers, "page": page, "per_page": perPage, "total": total})
}

func (r *Router) adminCreateVoucher(c *fiber.Ctx) error {
	var input service.CreateVoucherInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	voucher, err := r.adminVoucherSvc.CreateVoucher(ctx, input)
	if err != nil {
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if err == service.ErrConflict {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Kode voucher sudah ada"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal membuat voucher"})
	}

	return c.Status(fiber.StatusCreated).JSON(voucher)
}

func (r *Router) adminGetVoucher(c *fiber.Ctx) error {
	id := c.Params("id")

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	voucher, err := r.adminVoucherSvc.GetVoucher(ctx, id)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Voucher tidak ditemukan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil data voucher"})
	}

	return c.JSON(voucher)
}

func (r *Router) adminUpdateVoucher(c *fiber.Ctx) error {
	id := c.Params("id")

	var input service.UpdateVoucherInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	voucher, err := r.adminVoucherSvc.UpdateVoucher(ctx, id, input)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Voucher tidak ditemukan"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if err == service.ErrConflict {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Kode voucher sudah ada"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal memperbarui voucher"})
	}

	return c.JSON(voucher)
}

func (r *Router) adminDeleteVoucher(c *fiber.Ctx) error {
	id := c.Params("id")

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	err := r.adminVoucherSvc.DeleteVoucher(ctx, id)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Voucher tidak ditemukan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal menghapus voucher"})
	}

	return c.JSON(fiber.Map{"message": "Voucher berhasil dihapus"})
}

func (r *Router) adminListInventoryAccounts(c *fiber.Ctx) error {
	filter := service.InventoryAccountFilter{
		ProductID: c.Query("product_id"),
		Active:    parseBoolPtr(c.Query("active")),
	}
	page := c.QueryInt("page", 1)
	perPage := c.QueryInt("per_page", 20)
	if perPage > 100 {
		perPage = 100
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	accounts, total, err := r.adminInventorySvc.ListInventoryAccounts(ctx, filter, page, perPage)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil daftar akun inventaris"})
	}

	// Never return encrypted credentials
	for i := range accounts {
		accounts[i].EncryptedCredentials = nil
	}

	return c.JSON(fiber.Map{"accounts": accounts, "page": page, "per_page": perPage, "total": total})
}

func (r *Router) adminCreateInventoryAccount(c *fiber.Ctx) error {
	var input service.CreateInventoryAccountInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	account, err := r.adminInventorySvc.CreateInventoryAccount(ctx, input)
	if err != nil {
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal menambahkan akun inventaris"})
	}

	account.EncryptedCredentials = nil
	return c.Status(fiber.StatusCreated).JSON(account)
}

func (r *Router) adminGetInventoryAccount(c *fiber.Ctx) error {
	id := c.Params("id")

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	account, err := r.adminInventorySvc.GetInventoryAccount(ctx, id)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Akun inventaris tidak ditemukan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil data akun inventaris"})
	}

	account.EncryptedCredentials = nil
	return c.JSON(account)
}

func (r *Router) adminUpdateInventoryAccount(c *fiber.Ctx) error {
	id := c.Params("id")

	var input service.UpdateInventoryAccountInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	account, err := r.adminInventorySvc.UpdateInventoryAccount(ctx, id, input)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Akun inventaris tidak ditemukan"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal memperbarui akun inventaris"})
	}

	account.EncryptedCredentials = nil
	return c.JSON(account)
}

func (r *Router) adminDeleteInventoryAccount(c *fiber.Ctx) error {
	id := c.Params("id")

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	err := r.adminInventorySvc.DeleteInventoryAccount(ctx, id)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Akun inventaris tidak ditemukan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal menghapus akun inventaris"})
	}

	return c.JSON(fiber.Map{"message": "Akun inventaris berhasil dihapus"})
}

func (r *Router) adminListPayments(c *fiber.Ctx) error {
	// Use order service to list payments (straightforward list for dashboard)
	filter := service.OrderFilter{
		UserID: c.Query("user_id"),
		Status: c.Query("status"),
		Search: c.Query("search"),
	}
	page := c.QueryInt("page", 1)
	perPage := c.QueryInt("per_page", 20)
	if perPage > 100 {
		perPage = 100
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	orders, total, err := r.adminOrderSvc.ListOrders(ctx, filter, page, perPage)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil daftar pembayaran"})
	}

	return c.JSON(fiber.Map{"payments": orders, "page": page, "per_page": perPage, "total": total})
}

func (r *Router) adminListNotifications(c *fiber.Ctx) error {
	// Use audit logs as notifications proxy for dashboard
	filter := service.AuditLogFilter{
		Action:   c.Query("action"),
		Entity:   c.Query("entity"),
		EntityID: c.Query("entity_id"),
		UserID:   c.Query("user_id"),
		FromDate: c.Query("from_date"),
		ToDate:   c.Query("to_date"),
	}
	page := c.QueryInt("page", 1)
	perPage := c.QueryInt("per_page", 50)
	if perPage > 200 {
		perPage = 200
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	logs, total, err := r.adminSvc.ListAuditLogs(ctx, filter, page, perPage)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil notifikasi"})
	}

	return c.JSON(fiber.Map{"notifications": logs, "page": page, "per_page": perPage, "total": total})
}
