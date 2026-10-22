package handler

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/service"
	"github.com/redis/go-redis/v9"
)

func parseBoolPtr(raw string) (*bool, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, service.ErrInvalidInput
	}
	return &value, nil
}

// parseNonNegativeInt64 turns an optional query value into an inclusive bound.
// An empty value means "no bound"; a malformed or negative value is a client
// error rather than a silently ignored filter.
func parseNonNegativeInt64(raw string) (*int64, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return nil, service.ErrInvalidInput
	}
	return &value, nil
}

func parseOptionalBool(raw string) (bool, error) {
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, service.ErrInvalidInput
	}
	return value, nil
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
	orderDeliverySvc  *service.OrderDeliveryService
	accountOTPSvc     *service.AccountOTPService
	reportSvc         *service.ReportService
	testimonialSvc    *service.TestimonialService
	tokens            *service.TokenManager
	cipher            *service.CredentialCipher
	redis             *redis.Client
	authCookies       AuthCookiePolicy
	oauthStates       OAuthStateStore
}

// AuthCookiePolicy is the single source of truth for every authentication
// cookie the API writes. Set and clear use the same values so a browser can
// always delete the cookie it received.
type AuthCookiePolicy struct {
	Secure   bool
	HTTPOnly bool
	SameSite string
	Domain   string
	Path     string
}

func defaultAuthCookiePolicy() AuthCookiePolicy {
	return AuthCookiePolicy{HTTPOnly: true, SameSite: "Lax", Path: "/api/v1/auth"}
}

// SetAuthCookiePolicy applies the operator's cookie configuration. Empty values
// fall back to safe defaults rather than emitting an unscoped cookie.
func (r *Router) SetAuthCookiePolicy(policy AuthCookiePolicy) {
	if policy.SameSite == "" {
		policy.SameSite = "Lax"
	}
	if policy.Path == "" {
		policy.Path = "/api/v1/auth"
	}
	// HttpOnly is not optional for a refresh-token cookie, even if an operator
	// tries to disable it.
	policy.HTTPOnly = true
	r.authCookies = policy
}

// SetOAuthStateStore replaces the Redis-backed state store. Tests use this to
// exercise replay, expiry and outage behaviour without a live Redis.
func (r *Router) SetOAuthStateStore(store OAuthStateStore) { r.oauthStates = store }

// SetDeliveryService wires the W17 account-delivery flow. It is a setter rather
// than a NewRouter parameter so existing router tests keep their compact
// constructor; a nil service simply leaves the delivery routes disabled.
func (r *Router) SetDeliveryService(svc *service.OrderDeliveryService) { r.orderDeliverySvc = svc }

// SetAccountOTPService wires the public email-OTP lookup. A nil service keeps
// the lookup route disabled so a deployment without a mailbox cannot pretend
// to retrieve codes.
func (r *Router) SetAccountOTPService(svc *service.AccountOTPService) { r.accountOTPSvc = svc }

func NewRouter(authSvc *service.AuthService, catalogSvc *service.CatalogService, cartSvc *service.CartService, checkoutSvc *service.CheckoutService, webhookSvc *service.WebhookService, orderSvc *service.OrderService, adminSvc *service.AdminDashboardService, adminUserSvc *service.AdminUserService, adminProductSvc *service.AdminProductService, adminCategorySvc *service.AdminCategoryService, adminOrderSvc *service.AdminOrderService, adminVoucherSvc *service.AdminVoucherService, adminInventorySvc *service.AdminInventoryService, tokens *service.TokenManager, cipher *service.CredentialCipher, redis *redis.Client, reportSvc *service.ReportService, testimonialSvc *service.TestimonialService) *Router {
	r := &Router{authSvc: authSvc, catalogSvc: catalogSvc, cartSvc: cartSvc, checkoutSvc: checkoutSvc, webhookSvc: webhookSvc, orderSvc: orderSvc, adminSvc: adminSvc, adminUserSvc: adminUserSvc, adminProductSvc: adminProductSvc, adminCategorySvc: adminCategorySvc, adminOrderSvc: adminOrderSvc, adminVoucherSvc: adminVoucherSvc, adminInventorySvc: adminInventorySvc, reportSvc: reportSvc, testimonialSvc: testimonialSvc, tokens: tokens, cipher: cipher, redis: redis, authCookies: defaultAuthCookiePolicy()}
	if redis != nil {
		r.oauthStates = newRedisOAuthStateStore(redis)
	}
	return r
}

// authError writes the shared sanitized error envelope. Internal error values
// are never serialized: only this fixed message and machine code reach clients.
func authError(c *fiber.Ctx, status int, message, code string) error {
	return c.Status(status).JSON(fiber.Map{"error": message, "code": code})
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
	// Email-OTP retrieval: the buyer proves ownership with the account email and
	// the short-lived code generated from the order page. No session is needed
	// because the customer may be signing in on another device. It must sit on
	// the API root, not the /public group: the storefront and the per-route rate
	// limiter both use POST /api/v1/account-otp/lookup.
	api.Post("/account-otp/lookup", r.lookupAccountOTP)
	// QA poin 16: halaman "Testimoni" reads the approved reviews. Public because
	// the page is reachable without signing in.
	public.Get("/testimonials", r.listTestimonials)
	// Lets the storefront show a provider button only when it can actually work,
	// so enabling Google later needs no frontend rebuild.
	public.Get("/auth-providers", r.listAuthProviders)
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
	// Purchased-account access and the email-OTP access code are owner-only.
	protected.Get("/orders/:id/account-access", r.getAccountAccess)
	protected.Post("/orders/:id/account-access/:itemId/code", r.issueAccountAccessCode)
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
	// Detail baca-saja untuk operator. Service dan repository sudah menyediakan
	// GetOrder; route ini hanya mengeksposnya dengan bentuk order C-02 yang sama.
	admin.Get("/orders/:id", r.adminGetOrder)
	admin.Patch("/orders/:id/status", r.adminUpdateOrderStatus)
	// Aksi eksplisit "Setujui & kirim akun". Status processing dari PATCH juga
	// memicu pengiriman, tetapi endpoint ini membuat niatnya jelas di UI dan
	// aman dipanggil ulang (delivery sent tidak dikirim dua kali).
	admin.Post("/orders/:id/approve-delivery", r.adminApproveOrderDelivery)
	admin.Get("/orders/:id/deliveries", r.adminListOrderDeliveries)
	admin.Post("/orders/:id/deliveries/:deliveryId/retry", r.adminRetryOrderDelivery)
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
	// Kapasitas jual sebuah paket: satu baris inventory_slots per kursi.
	admin.Post("/inventory/slots", r.adminCreateInventorySlots)
	admin.Get("/inventory/:id", r.adminGetInventoryAccount)
	admin.Get("/inventory/:id/slots", r.adminListInventorySlots)
	// Kode TOTP aktif untuk satu akun. Hanya admin, tanpa secret di response.
	admin.Post("/inventory/:id/otp", r.adminGenerateInventoryOTP)
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
			return authError(c, fiber.StatusUnauthorized, "Header otorisasi tidak ditemukan", "unauthorized")
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			return authError(c, fiber.StatusUnauthorized, "Format otorisasi tidak valid", "unauthorized")
		}

		claims, err := r.tokens.ParseAccess(parts[1])
		if err != nil {
			return authError(c, fiber.StatusUnauthorized, "Token tidak valid atau telah kedaluwarsa", "unauthorized")
		}

		// The JWT is only proof of past authentication. Authorization reads the
		// account and sign-in row from the database on every request, so a
		// disabled/deleted account, a demoted role or a revoked session is
		// rejected immediately instead of when the access token expires.
		ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
		defer cancel()
		user, err := r.authSvc.AuthorizeSession(ctx, claims.Subject, claims.SessionID)
		if err != nil {
			return authError(c, fiber.StatusUnauthorized, "Sesi tidak valid atau telah berakhir, silakan masuk kembali", "unauthorized")
		}

		c.Locals("user_id", user.ID)
		c.Locals("user_role", string(user.Role))
		c.Locals("session_id", claims.SessionID)
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
			return authError(c, fiber.StatusForbidden, "Akses ditolak: Anda tidak memiliki izin untuk tindakan ini", "forbidden")
		}
		return c.Next()
	}
}

func (r *Router) register(c *fiber.Ctx) error {
	var input service.RegisterInput
	if err := c.BodyParser(&input); err != nil {
		return authError(c, fiber.StatusBadRequest, "Format data permintaan tidak valid", "invalid_input")
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
			return authError(c, fiber.StatusConflict, "Email sudah terdaftar", "conflict")
		case errors.Is(err, service.ErrInvalidInput):
			return authError(c, fiber.StatusBadRequest, err.Error(), "invalid_input")
		case errors.Is(err, service.ErrMailUnavailable):
			// Do not reveal whether the account was created; the registration
			// check runs before any write, so nothing was created here.
			return authError(c, fiber.StatusServiceUnavailable, "Pendaftaran belum dapat diproses. Silakan coba lagi nanti.", "service_unavailable")
		}
		log.Printf("register failed for %q: %v", input.Email, err)
		return authError(c, fiber.StatusInternalServerError, "Pendaftaran akun gagal", "internal_error")
	}

	return c.Status(fiber.StatusCreated).JSON(result)
}

func (r *Router) login(c *fiber.Ctx) error {
	var input service.LoginInput
	if err := c.BodyParser(&input); err != nil {
		return authError(c, fiber.StatusBadRequest, "Format data permintaan tidak valid", "invalid_input")
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	session, err := r.authSvc.Login(ctx, input, service.ClientInfo{
		IPAddress: c.IP(),
		UserAgent: c.Get("User-Agent"),
	})
	if err != nil {
		if errors.Is(err, service.ErrUnauthorized) {
			return authError(c, fiber.StatusUnauthorized, "Email atau kata sandi tidak valid", "unauthorized")
		}
		if errors.Is(err, service.ErrEmailNotVerified) {
			return authError(c, fiber.StatusUnauthorized, service.ErrEmailNotVerified.Error(), "unauthorized")
		}
		return authError(c, fiber.StatusInternalServerError, "Gagal masuk ke akun", "internal_error")
	}

	r.setRefreshCookie(c, session.RefreshToken, session.RefreshExpiresAt)

	return c.JSON(fiber.Map{
		"user":         session.User,
		"access_token": session.AccessToken,
		"expires_in":   session.ExpiresIn,
	})
}

func (r *Router) refresh(c *fiber.Ctx) error {
	refreshToken := c.Cookies("refresh_token")
	if refreshToken == "" {
		return authError(c, fiber.StatusUnauthorized, "Token penyegar tidak ditemukan", "unauthorized")
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	session, err := r.authSvc.Refresh(ctx, refreshToken, service.ClientInfo{
		IPAddress: c.IP(),
		UserAgent: c.Get("User-Agent"),
	})
	if err != nil {
		// The cookie is deliberately left alone. Refresh rotates the token, so a
		// second tab presenting the previous one gets a 401 — and deleting the
		// cookie here would then destroy the replacement that tab was never told
		// about, signing the whole browser out. An unusable cookie simply fails
		// again on the next attempt; a valid one survives.
		if errors.Is(err, service.ErrUnauthorized) {
			return authError(c, fiber.StatusUnauthorized, "Sesi telah kedaluwarsa atau dicabut, silakan masuk kembali", "unauthorized")
		}
		return authError(c, fiber.StatusInternalServerError, "Gagal memperbarui sesi", "internal_error")
	}

	r.setRefreshCookie(c, session.RefreshToken, session.RefreshExpiresAt)

	return c.JSON(fiber.Map{
		"user":         session.User,
		"access_token": session.AccessToken,
		"expires_in":   session.ExpiresIn,
	})
}

// writeAuthCookie is the one place authentication cookies are created. Set and
// clear use identical attributes, so clearing always targets the cookie the
// browser actually stored. sameSite overrides the configured policy when the
// browser context requires it (the OAuth nonce must survive a cross-site
// top-level navigation back from Google).
func (r *Router) writeAuthCookie(c *fiber.Ctx, name, value, path string, maxAge int, expires time.Time, sameSite string) {
	if path == "" {
		path = r.authCookies.Path
	}
	if sameSite == "" {
		sameSite = r.authCookies.SameSite
	}
	c.Cookie(&fiber.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		Domain:   r.authCookies.Domain,
		MaxAge:   maxAge,
		Expires:  expires,
		HTTPOnly: true,
		// The configured policy governs production. A TLS-terminating proxy can
		// make the request look like plain HTTP, so also honour the request
		// scheme when it reports HTTPS.
		Secure:   r.authCookies.Secure || c.Protocol() == "https",
		SameSite: sameSite,
	})
}

// appendLegacyRefreshCookieClear targets the Path=/ cookie written by releases
// before the cookie path became configurable. A Set-Cookie with a different Path
// does not replace the old cookie, so without this a legacy cookie would survive
// logout and could mint a new session through /auth/refresh.
//
// Fiber/fasthttp stores cookies keyed by name, so a second c.Cookie call would
// replace the new cookie instead of adding a deletion for the other path. The
// legacy deletion is therefore appended as a separate Set-Cookie header after
// the primary cookie has been written.
func (r *Router) appendLegacyRefreshCookieClear(c *fiber.Ctx) {
	if r.authCookies.Path == "/" {
		return
	}
	sameSite := http.SameSiteLaxMode
	switch strings.ToLower(r.authCookies.SameSite) {
	case "strict":
		sameSite = http.SameSiteStrictMode
	case "none":
		sameSite = http.SameSiteNoneMode
	}
	base := http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Now().Add(-time.Hour),
		Secure:   r.authCookies.Secure || c.Protocol() == "https",
		HttpOnly: true,
		SameSite: sameSite,
	}
	// The pre-upgrade release wrote a host-only cookie at Path=/. That identity
	// has no Domain attribute, so it must be cleared by a header that also has
	// no Domain; otherwise the browser keeps the legacy cookie even when
	// COOKIE_DOMAIN is configured for the new cookie.
	c.Response().Header.Add("Set-Cookie", base.String())
	// Clear a domain-scoped Path=/ variant too, for deployments that may have
	// written one in an intermediate release.
	if r.authCookies.Domain != "" {
		domainCookie := base
		domainCookie.Domain = r.authCookies.Domain
		c.Response().Header.Add("Set-Cookie", domainCookie.String())
	}
}

// setRefreshCookie writes the session cookie. The lifetime comes from the
// configured refresh TTL, so raising JWT_REFRESH_TTL no longer leaves the cookie
// expiring at a hardcoded week.
func (r *Router) setRefreshCookie(c *fiber.Ctx, token string, expiresAt time.Time) {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge <= 0 {
		maxAge = int((7 * 24 * time.Hour).Seconds())
	}
	r.writeAuthCookie(c, "refresh_token", token, "", maxAge, time.Time{}, "")
	// Retire the pre-upgrade cookie on every set, not only on logout, so the
	// two-cookie state disappears as soon as the user signs in or refreshes.
	r.appendLegacyRefreshCookieClear(c)
}

func (r *Router) clearRefreshCookie(c *fiber.Ctx) {
	r.writeAuthCookie(c, "refresh_token", "", "", -1, time.Now().Add(-time.Hour), "")
	r.appendLegacyRefreshCookieClear(c)
}

func (r *Router) setOAuthNonceCookie(c *fiber.Ctx, nonce string, ttl time.Duration) {
	maxAge := int(ttl.Seconds())
	if maxAge <= 0 {
		maxAge = int((10 * time.Minute).Seconds())
	}
	// The Google callback is a cross-site top-level GET. SameSite=Strict would
	// suppress this cookie and make every OAuth login fail with a 401, so the
	// nonce is deliberately Lax regardless of the refresh-cookie policy.
	r.writeAuthCookie(c, "oauth_nonce", nonce, "/api/v1/auth/google", maxAge, time.Time{}, "Lax")
}

func (r *Router) clearOAuthNonceCookie(c *fiber.Ctx) {
	r.writeAuthCookie(c, "oauth_nonce", "", "/api/v1/auth/google", -1, time.Now().Add(-time.Hour), "Lax")
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
	r.clearRefreshCookie(c)
	return c.JSON(fiber.Map{"message": "Berhasil keluar"})
}

func (r *Router) changePassword(c *fiber.Ctx) error {
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := c.BodyParser(&input); err != nil {
		return authError(c, fiber.StatusBadRequest, "Format data permintaan tidak valid", "invalid_input")
	}
	userID, _ := c.Locals("user_id").(string)
	sessionID, _ := c.Locals("session_id").(string)

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	err := r.authSvc.ChangePassword(ctx, userID, input.CurrentPassword, input.NewPassword, c.Cookies("refresh_token"), sessionID, service.ClientInfo{
		IPAddress: c.IP(),
		UserAgent: c.Get("User-Agent"),
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrUnauthorized):
			return authError(c, fiber.StatusUnauthorized, "Sesi tidak valid atau telah berakhir, silakan masuk kembali", "unauthorized")
		case errors.Is(err, service.ErrInvalidInput):
			return authError(c, fiber.StatusBadRequest, err.Error(), "invalid_input")
		case errors.Is(err, service.ErrNotFound):
			return authError(c, fiber.StatusNotFound, "Akun tidak ditemukan", "not_found")
		}
		log.Printf("change-password failed for %q: %v", userID, err)
		return authError(c, fiber.StatusInternalServerError, "Kata sandi gagal diubah", "internal_error")
	}
	return c.JSON(fiber.Map{"message": "Kata sandi berhasil diubah. Sesi lain sudah diakhiri."})
}

func (r *Router) listSessions(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	sessions, err := r.authSvc.ListSessions(ctx, userID, c.Cookies("refresh_token"))
	if err != nil {
		return authError(c, fiber.StatusInternalServerError, "Gagal mengambil daftar sesi", "internal_error")
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
			return authError(c, fiber.StatusBadRequest, err.Error(), "invalid_input")
		case errors.Is(err, service.ErrNotFound):
			return authError(c, fiber.StatusNotFound, "Sesi tidak ditemukan atau sudah berakhir", "not_found")
		}
		return authError(c, fiber.StatusInternalServerError, "Sesi gagal diakhiri", "internal_error")
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
		return authError(c, fiber.StatusInternalServerError, "Sesi lain gagal diakhiri", "internal_error")
	}
	return c.JSON(fiber.Map{"message": "Sesi lain sudah diakhiri", "ended": ended})
}

func (r *Router) verifyEmail(c *fiber.Ctx) error {
	var input struct {
		Token string `json:"token"`
	}
	if err := c.BodyParser(&input); err != nil {
		return authError(c, fiber.StatusBadRequest, "Format data permintaan tidak valid", "invalid_input")
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	user, err := r.authSvc.VerifyEmail(ctx, input.Token)
	if err != nil {
		if errors.Is(err, service.ErrUnauthorized) || errors.Is(err, service.ErrNotFound) {
			return authError(c, fiber.StatusBadRequest, "Token verifikasi tidak valid atau telah kedaluwarsa", "invalid_input")
		}
		return authError(c, fiber.StatusInternalServerError, "Verifikasi email gagal", "internal_error")
	}

	return c.JSON(fiber.Map{"message": "Email berhasil diverifikasi", "user": user})
}

func (r *Router) resendVerification(c *fiber.Ctx) error {
	var input struct {
		Email string `json:"email"`
	}
	if err := c.BodyParser(&input); err != nil {
		return authError(c, fiber.StatusBadRequest, "Format data permintaan tidak valid", "invalid_input")
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	if err := r.authSvc.ResendVerification(ctx, input.Email); err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidInput):
			return authError(c, fiber.StatusBadRequest, "Masukkan alamat email yang valid.", "invalid_input")
		case errors.Is(err, service.ErrNotFound):
			return authError(c, fiber.StatusNotFound, "Email belum terdaftar. Silakan membuat akun terlebih dahulu.", "not_found")
		case errors.Is(err, service.ErrAlreadyVerified):
			return authError(c, fiber.StatusConflict, err.Error(), "conflict")
		case errors.Is(err, service.ErrMailUnavailable):
			return authError(c, fiber.StatusServiceUnavailable, "Layanan email sedang tidak tersedia. Silakan coba lagi nanti.", "service_unavailable")
		}
		log.Printf("resend-verification failed for %q: %v", input.Email, err)
		return authError(c, fiber.StatusInternalServerError, "Gagal mengirim ulang tautan verifikasi.", "internal_error")
	}

	return c.JSON(fiber.Map{"message": "Tautan verifikasi baru sudah dikirim ke email Anda."})
}

func (r *Router) forgotPassword(c *fiber.Ctx) error {
	var input struct {
		Email string `json:"email"`
	}
	if err := c.BodyParser(&input); err != nil {
		return authError(c, fiber.StatusBadRequest, "Format data permintaan tidak valid", "invalid_input")
	}

	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()

	if err := r.authSvc.ForgotPassword(ctx, input.Email); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return authError(c, fiber.StatusNotFound, "Email belum terdaftar. Silakan membuat akun terlebih dahulu.", "not_found")
		}
		if errors.Is(err, service.ErrInvalidInput) {
			return authError(c, fiber.StatusBadRequest, "Masukkan alamat email yang valid.", "invalid_input")
		}
		if errors.Is(err, service.ErrMailUnavailable) {
			return authError(c, fiber.StatusServiceUnavailable, "Layanan email sedang tidak tersedia. Silakan coba lagi nanti.", "service_unavailable")
		}
		log.Printf("forgot-password failed for %q: %v", input.Email, err)
		return authError(c, fiber.StatusInternalServerError, "Permintaan atur ulang kata sandi gagal.", "internal_error")
	}

	return c.JSON(fiber.Map{"message": "Tautan atur ulang kata sandi sudah dikirim."})
}

func (r *Router) resetPassword(c *fiber.Ctx) error {
	var input struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := c.BodyParser(&input); err != nil {
		return authError(c, fiber.StatusBadRequest, "Format data permintaan tidak valid", "invalid_input")
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	user, err := r.authSvc.ResetPassword(ctx, input.Token, input.Password)
	if err != nil {
		if errors.Is(err, service.ErrUnauthorized) {
			return authError(c, fiber.StatusBadRequest, "Tautan atur ulang kata sandi tidak valid atau sudah kedaluwarsa. Silakan minta tautan baru.", "invalid_input")
		}
		if errors.Is(err, service.ErrInvalidInput) {
			return authError(c, fiber.StatusBadRequest, err.Error(), "invalid_input")
		}
		return authError(c, fiber.StatusInternalServerError, "Gagal mengatur ulang kata sandi", "internal_error")
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

func (r *Router) listAuthProviders(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"password": true,
		"google":   r.authSvc.GoogleConfig() != nil,
	})
}

func (r *Router) googleLogin(c *fiber.Ctx) error {
	if r.authSvc.GoogleConfig() == nil {
		return authError(c, fiber.StatusServiceUnavailable, "Autentikasi Google belum dikonfigurasi", "service_unavailable")
	}
	cfg := r.authSvc.GoogleConfig()
	// The nonce lives in a browser cookie and the state in Redis. Binding the
	// two means a state value observed by another client (even on the same IP)
	// cannot be redeemed from a different browser.
	state, err := r.authSvc.GenerateOAuthState()
	if err != nil {
		return authError(c, fiber.StatusInternalServerError, "Gagal membuat state autentikasi Google", "internal_error")
	}
	nonce, err := r.authSvc.GenerateOAuthState()
	if err != nil {
		return authError(c, fiber.StatusInternalServerError, "Gagal membuat state autentikasi Google", "internal_error")
	}
	if r.oauthStates == nil {
		// State storage is mandatory. Without it the callback cannot prove that
		// this browser started the flow, so fail closed instead of skipping the
		// check.
		return authError(c, fiber.StatusServiceUnavailable, "Layanan masuk dengan Google sedang tidak tersedia", "service_unavailable")
	}
	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	defer cancel()
	if err := r.oauthStates.Save(ctx, state, nonce, cfg.StateTTL); err != nil {
		return authError(c, fiber.StatusServiceUnavailable, "Layanan masuk dengan Google sedang tidak tersedia", "service_unavailable")
	}
	r.setOAuthNonceCookie(c, nonce, cfg.StateTTL)
	authURL := fmt.Sprintf("%s?client_id=%s&redirect_uri=%s&response_type=code&scope=openid%%20email%%20profile&access_type=offline&prompt=consent&state=%s",
		cfg.AuthURL, url.QueryEscape(cfg.ClientID), url.QueryEscape(cfg.RedirectURL), state)
	return c.Redirect(authURL, fiber.StatusTemporaryRedirect)
}

func (r *Router) googleCallback(c *fiber.Ctx) error {
	if r.authSvc.GoogleConfig() == nil {
		return authError(c, fiber.StatusServiceUnavailable, "Autentikasi Google belum dikonfigurasi", "service_unavailable")
	}
	cfg := r.authSvc.GoogleConfig()
	code := c.Query("code")
	state := c.Query("state")
	nonce := c.Cookies("oauth_nonce")
	if code == "" || state == "" {
		return authError(c, fiber.StatusBadRequest, "Kode otorisasi atau state Google tidak lengkap", "invalid_input")
	}
	if r.oauthStates == nil {
		return authError(c, fiber.StatusServiceUnavailable, "Layanan masuk dengan Google sedang tidak tersedia", "service_unavailable")
	}
	// Consume before exchanging the code: a state is valid at most once, even
	// if the provider call below fails. GetDel makes concurrent callbacks
	// race-safe.
	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	storedNonce, err := r.oauthStates.Consume(ctx, state)
	cancel()
	// Clear the nonce regardless of the outcome so it cannot be reused.
	r.clearOAuthNonceCookie(c)
	if err != nil {
		if errors.Is(err, ErrOAuthStateNotFound) {
			return authError(c, fiber.StatusUnauthorized, "Sesi login Google tidak valid atau kedaluwarsa", "unauthorized")
		}
		return authError(c, fiber.StatusServiceUnavailable, "Layanan masuk dengan Google sedang tidak tersedia", "service_unavailable")
	}
	if nonce == "" || subtle.ConstantTimeCompare([]byte(storedNonce), []byte(nonce)) != 1 {
		return authError(c, fiber.StatusUnauthorized, "Sesi login Google tidak valid atau kedaluwarsa", "unauthorized")
	}
	exchangeCtx, exchangeCancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer exchangeCancel()
	session, err := r.authSvc.ExchangeGoogleCode(exchangeCtx, code, cfg)
	if err != nil {
		if errors.Is(err, service.ErrUnauthorized) {
			return authError(c, fiber.StatusUnauthorized, "Akun Google tidak dapat digunakan untuk masuk", "unauthorized")
		}
		return authError(c, fiber.StatusInternalServerError, "Gagal menghubungkan akun Google", "internal_error")
	}
	// The session cookie is set here and the tokens stay out of the redirect: a
	// query string ends up in browser history, the Referer header, and every
	// proxy log along the way. The storefront recovers the session on load by
	// calling /auth/refresh, which sends this cookie.
	r.setRefreshCookie(c, session.RefreshToken, session.RefreshExpiresAt)
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

	minPrice, err := parseNonNegativeInt64(c.Query("min_price"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Harga minimum tidak valid", "code": "invalid_input"})
	}
	maxPrice, err := parseNonNegativeInt64(c.Query("max_price"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Harga maksimum tidak valid", "code": "invalid_input"})
	}
	inStock, err := parseOptionalBool(c.Query("in_stock"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Filter ketersediaan tidak valid", "code": "invalid_input"})
	}

	active := true
	filter := service.ProductFilter{
		CategoryID: c.Query("category"),
		Search:     c.Query("search"),
		Active:     &active,
		MinPrice:   minPrice,
		MaxPrice:   maxPrice,
		InStock:    inStock,
	}
	page := c.QueryInt("page", 1)
	perPage := c.QueryInt("per_page", 20)
	if perPage > 100 {
		perPage = 100
	}
	products, total, err := r.catalogSvc.ListProductsWithStock(ctx, filter, page, perPage)
	if err != nil {
		if errors.Is(err, service.ErrInvalidInput) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error(), "code": "invalid_input"})
		}
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

	// Sama seperti getProduct: id bisa berupa slug. product_id bertipe uuid, jadi
	// slug yang diteruskan apa adanya membuat Postgres melempar SQLSTATE 22P02
	// dan permintaan dijawab 500 padahal produknya hanya tidak ditemukan.
	if _, err := uuid.Parse(id); err != nil {
		product, err := r.catalogSvc.GetProductBySlugWithStock(ctx, id)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Produk langganan tidak ditemukan"})
		}
		id = product.ID
	}

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
		if errors.Is(err, service.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Paket langganan tidak ditemukan"})
		}
		if errors.Is(err, service.ErrInvalidInput) {
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
		if errors.Is(err, service.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Item keranjang tidak ditemukan"})
		}
		if errors.Is(err, service.ErrInvalidInput) {
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
		if errors.Is(err, service.ErrNotFound) {
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
		// The caller only sees a generic 400, so record the reason here.
		log.Printf("checkout: body parse failed: %v", err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}
	input.UserID = userID
	// The storefront sends the idempotency key as an Idempotency-Key header, and
	// nothing ever read it into the service input. The field therefore stayed
	// empty and the service's very first validation rejected every checkout with
	// a 400 — before a single database query, which is why it failed in ~100µs.
	if header := strings.TrimSpace(c.Get("Idempotency-Key")); header != "" {
		input.IdempotencyKey = header
	}

	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()

	result, err := r.checkoutSvc.Checkout(ctx, input)
	if err != nil {
		return writeCheckoutError(c, err)
	}

	// 201 on the first logical checkout, 200 with the identical stored body on
	// an idempotent replay. The body itself is the same contract either way.
	status := fiber.StatusCreated
	if result.Replayed {
		status = fiber.StatusOK
	}
	return c.Status(status).JSON(result)
}

func writeCheckoutError(c *fiber.Ctx, err error) error {
	var priceChanged *service.PriceChangedError
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(), "code": "invalid_input",
		})
	case errors.Is(err, service.ErrNotFound):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Produk atau voucher tidak ditemukan", "code": "not_found",
		})
	case errors.Is(err, service.ErrUnsupportedPaymentChannel):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(), "code": "unsupported_payment_channel",
		})
	case errors.Is(err, service.ErrPaymentChannelNotEligible):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(), "code": "payment_channel_not_eligible",
		})
	case errors.Is(err, service.ErrOutOfStock):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "Slot akun langganan tidak mencukupi atau sedang habis", "code": "out_of_stock",
		})
	case errors.Is(err, service.ErrIdempotencyConflict):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": err.Error(), "code": "idempotency_conflict",
		})
	case errors.Is(err, service.ErrCheckoutInProgress):
		// No mutation: the caller can retry the same owner/key and will receive
		// the stored result once the in-flight attempt finishes.
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": err.Error(), "code": "idempotency_conflict",
		})
	case errors.As(err, &priceChanged):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": priceChanged.Error(), "code": "price_changed",
			"current_subtotal": priceChanged.CurrentSubtotal, "currency": priceChanged.Currency,
		})
	default:
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Proses pemesanan gagal"})
	}
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

func (r *Router) getAccountAccess(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	id := c.Params("id")

	if r.orderDeliverySvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "Akses akun belum tersedia", "code": "account_access_unavailable",
		})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 8*time.Second)
	defer cancel()

	items, err := r.orderDeliverySvc.ListAccountAccess(ctx, userID, id)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotFound):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Pesanan tidak ditemukan", "code": "not_found"})
		case errors.Is(err, service.ErrOrderNotApproved):
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": "Akun belum dikirim. Akses OTP tersedia setelah admin menyetujui pesanan.",
				"code":  "order_not_approved",
			})
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil akses akun", "code": "internal_error"})
		}
	}

	otpReady := r.accountOTPSvc != nil && r.accountOTPSvc.InboxReady()
	return c.JSON(fiber.Map{"items": items, "otp_inbox_ready": otpReady})
}

func (r *Router) issueAccountAccessCode(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	orderID := c.Params("id")
	itemID := c.Params("itemId")

	if r.orderDeliverySvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "Akses OTP belum tersedia", "code": "otp_unavailable",
		})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 25*time.Second)
	defer cancel()

	result, err := r.orderDeliverySvc.IssueAccountAccessCode(ctx, userID, orderID, itemID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrOTPUnavailable):
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"error": "Layanan ambil OTP belum dikonfigurasi. Hubungi dukungan.", "code": "otp_unavailable",
			})
		case errors.Is(err, service.ErrOrderNotApproved):
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": "Kode akses hanya dapat dibuat setelah akun dikirim admin.", "code": "order_not_approved",
			})
		case errors.Is(err, service.ErrNotFound):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Akun tidak ditemukan pada pesanan ini", "code": "not_found"})
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal membuat kode akses OTP", "code": "internal_error"})
		}
	}

	return c.JSON(result)
}

func (r *Router) lookupAccountOTP(c *fiber.Ctx) error {
	var input struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := c.BodyParser(&input); err != nil {
		return authError(c, fiber.StatusBadRequest, "Data permintaan tidak valid", "invalid_input")
	}
	input.Email = strings.TrimSpace(input.Email)
	input.Code = strings.TrimSpace(input.Code)
	if input.Email == "" || len(input.Email) > 254 || input.Code == "" || len(input.Code) > 32 {
		return authError(c, fiber.StatusBadRequest, "Masukkan email akun dan kode akses yang valid", "invalid_input")
	}
	if r.accountOTPSvc == nil {
		return authError(c, fiber.StatusServiceUnavailable, "Layanan ambil OTP belum tersedia", "otp_unavailable")
	}

	ctx, cancel := context.WithTimeout(c.Context(), 25*time.Second)
	defer cancel()

	message, err := r.accountOTPSvc.Lookup(ctx, input.Email, input.Code)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrOTPAccessInvalid):
			// One message for a wrong email and a wrong code prevents account
			// enumeration.
			return authError(c, fiber.StatusNotFound, "Email atau kode akses tidak cocok. Periksa kembali lalu coba lagi.", "otp_access_invalid")
		case errors.Is(err, service.ErrOTPUnavailable):
			return authError(c, fiber.StatusServiceUnavailable, "Layanan ambil OTP belum dikonfigurasi. Hubungi dukungan.", "otp_unavailable")
		case errors.Is(err, service.ErrOTPNotFound):
			return authError(c, fiber.StatusNotFound, "Belum ada email OTP terbaru untuk akun ini. Tunggu beberapa saat lalu coba lagi.", "otp_not_found")
		default:
			return authError(c, fiber.StatusServiceUnavailable, "Gagal membaca email OTP. Coba lagi sebentar lagi.", "otp_lookup_failed")
		}
	}

	return c.JSON(fiber.Map{
		"otp":         message.Code,
		"received_at": message.ReceivedAt,
	})
}

func (r *Router) getOrder(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	id := c.Params("id")

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	order, err := r.orderSvc.GetOrder(ctx, userID, id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
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
	active, err := parseBoolPtr(c.Query("active"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Filter status pengguna tidak valid", "code": "invalid_input"})
	}
	filter := service.UserFilter{
		Role:   c.Query("role"),
		Active: active,
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
		if errors.Is(err, service.ErrInvalidInput) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Filter peran pengguna tidak valid", "code": "invalid_input"})
		}
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
		if errors.Is(err, service.ErrNotFound) {
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
		Name   *string `json:"name"`
		Email  *string `json:"email"`
		Active *bool   `json:"active"`
		Role   *string `json:"role"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid", "code": "invalid_input"})
	}

	update := service.AdminUserUpdate{
		Name:   input.Name,
		Email:  input.Email,
		Active: input.Active,
	}
	// An omitted role stays nil (no-op), but an explicitly empty string is a
	// value, not an omission. Let the service reject it as invalid input
	// instead of silently treating it as "unchanged".
	if input.Role != nil {
		role := domain.UserRole(*input.Role)
		update.Role = &role
	}

	actorID, _ := c.Locals("user_id").(string)
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	user, err := r.adminUserSvc.UpdateUser(ctx, service.AdminActor{UserID: actorID, IPAddress: c.IP()}, id, update)
	if err != nil {
		return adminUserError(c, err)
	}

	user.PasswordHash = ""
	return c.JSON(user)
}

func (r *Router) deleteUser(c *fiber.Ctx) error {
	id := c.Params("id")
	actorID, _ := c.Locals("user_id").(string)

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	err := r.adminUserSvc.DeleteUser(ctx, service.AdminActor{UserID: actorID, IPAddress: c.IP()}, id)
	if err != nil {
		return adminUserError(c, err)
	}

	return c.JSON(fiber.Map{"message": "Pengguna berhasil dihapus"})
}

// adminUserError maps the service's typed access errors to an explicit status
// and machine code. Previously every failure except 404 collapsed into 500,
// which made a guard rejection look like a server outage to the dashboard.
func adminUserError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, service.ErrNotFound):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Pengguna tidak ditemukan", "code": "not_found"})
	case errors.Is(err, service.ErrInvalidInput):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Data pengguna tidak valid: peran harus customer, seller, atau admin", "code": "invalid_input"})
	case errors.Is(err, service.ErrConflict):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Email sudah digunakan oleh akun lain", "code": "conflict"})
	case errors.Is(err, service.ErrSelfRoleChange), errors.Is(err, service.ErrSelfDeactivate), errors.Is(err, service.ErrSelfDelete):
		return authError(c, fiber.StatusForbidden, err.Error(), "forbidden")
	case errors.Is(err, service.ErrLastAdmin):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error(), "code": "last_admin"})
	default:
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal memproses data pengguna"})
	}
}

func (r *Router) adminListProducts(c *fiber.Ctx) error {
	active, err := parseBoolPtr(c.Query("active"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Filter status produk tidak valid", "code": "invalid_input"})
	}
	filter := service.AdminProductFilter{
		CategoryID: c.Query("category_id"),
		Active:     active,
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
		if errors.Is(err, service.ErrInvalidInput) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error(), "code": "invalid_input"})
		}
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
		ComingSoon  bool   `json:"coming_soon"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	product, err := r.adminProductSvc.CreateProduct(ctx, input.CategoryID, input.Name, input.Slug, input.Description, input.ImageURL, input.ComingSoon)
	if err != nil {
		if errors.Is(err, service.ErrInvalidInput) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error(), "code": "invalid_input"})
		}
		if errors.Is(err, service.ErrConflict) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "SKU/Slug produk sudah digunakan", "code": "conflict"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal menambahkan produk"})
	}

	return c.Status(fiber.StatusCreated).JSON(product)
}

func (r *Router) adminUpdateProduct(c *fiber.Ctx) error {
	id := c.Params("id")

	var input struct {
		CategoryID  string  `json:"category_id"`
		Name        string  `json:"name"`
		Slug        string  `json:"slug"`
		Description string  `json:"description"`
		ImageURL    *string `json:"image_url"`
		Active      *bool   `json:"active"`
		ComingSoon  *bool   `json:"coming_soon"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	product, err := r.adminProductSvc.UpdateProduct(ctx, id, input.CategoryID, input.Name, input.Slug, input.Description, input.ImageURL, input.Active, input.ComingSoon)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Produk langganan tidak ditemukan", "code": "not_found"})
		}
		if errors.Is(err, service.ErrInvalidInput) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error(), "code": "invalid_input"})
		}
		if errors.Is(err, service.ErrConflict) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "SKU/Slug produk sudah digunakan", "code": "conflict"})
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
		if errors.Is(err, service.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Produk langganan tidak ditemukan", "code": "not_found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal menghapus produk"})
	}

	return c.JSON(fiber.Map{"message": "Produk berhasil dihapus"})
}

func (r *Router) adminListCategories(c *fiber.Ctx) error {
	active, err := parseBoolPtr(c.Query("active"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Filter status kategori tidak valid", "code": "invalid_input"})
	}
	filter := service.CategoryFilter{
		Active: active,
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
		if errors.Is(err, service.ErrInvalidInput) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error(), "code": "invalid_input"})
		}
		if errors.Is(err, service.ErrConflict) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Slug kategori sudah digunakan", "code": "conflict"})
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
		if errors.Is(err, service.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Kategori tidak ditemukan", "code": "not_found"})
		}
		if errors.Is(err, service.ErrInvalidInput) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error(), "code": "invalid_input"})
		}
		if errors.Is(err, service.ErrConflict) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Slug kategori sudah digunakan", "code": "conflict"})
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
		if errors.Is(err, service.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Kategori tidak ditemukan", "code": "not_found"})
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

func (r *Router) adminGetOrder(c *fiber.Ctx) error {
	id := c.Params("id")
	// id bertipe uuid di database. Menolak bentuk lain di sini mencegah
	// SQLSTATE 22P02 berubah menjadi 500 padahal pesanannya tidak ditemukan.
	if _, err := uuid.Parse(id); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Pesanan tidak ditemukan", "code": "not_found"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	order, err := r.adminOrderSvc.GetOrder(ctx, id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Pesanan tidak ditemukan", "code": "not_found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil detail pesanan"})
	}

	// W18 needs delivery status next to the order detail. The ledger carries no
	// credentials, so attaching it here is safe for the admin view.
	if r.orderDeliverySvc != nil {
		if deliveries, derr := r.orderDeliverySvc.ListDeliveries(ctx, order.ID); derr == nil {
			order.Deliveries = deliveries
		} else if !errors.Is(derr, service.ErrNotFound) {
			log.Printf("admin order detail: list deliveries for %s failed: %v", order.ID, derr)
		}
	}

	return c.JSON(order)
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

	// Approval may wait for the SMTP transaction, so processing gets a longer
	// budget than an ordinary status edit.
	timeout := 5 * time.Second
	if domain.OrderStatus(input.Status) == domain.OrderProcessing {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(c.Context(), timeout)
	defer cancel()

	order, err := r.adminOrderSvc.UpdateOrderStatus(ctx, id, domain.OrderStatus(input.Status))
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Pesanan tidak ditemukan"})
		}
		if errors.Is(err, service.ErrInvalidInput) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error(), "code": "invalid_input"})
		}
		if errors.Is(err, service.ErrInvalidTransition) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error(), "code": "invalid_transition"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal memperbarui status pesanan"})
	}

	// Moving an order to processing is the admin approval that releases the
	// purchased accounts. A failure is recorded on the delivery rows and shown
	// in the detail response; the status change itself stays committed.
	if r.orderDeliverySvc != nil && order.Status == domain.OrderProcessing {
		actor := service.AdminActor{UserID: localUserID(c), IPAddress: c.IP()}
		if deliveries, derr := r.orderDeliverySvc.DeliverApprovedOrder(ctx, actor, order.ID); derr != nil {
			log.Printf("order approval: delivery for %s failed: %v", order.ID, derr)
		} else {
			order.Deliveries = deliveries
		}
	}

	return c.JSON(order)
}

// adminApproveOrderDelivery is the explicit "Setujui & kirim akun" action. It
// applies the same paid→processing transition as the status endpoint, then
// runs the idempotent delivery pass and returns the resulting ledger.
func (r *Router) adminApproveOrderDelivery(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Pesanan tidak ditemukan", "code": "not_found"})
	}
	if r.orderDeliverySvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Fitur pengiriman akun belum dikonfigurasi", "code": "delivery_unavailable"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
	defer cancel()

	order, err := r.adminOrderSvc.GetOrder(ctx, id)
	if err != nil {
		return orderDeliveryError(c, err)
	}
	switch order.Status {
	case domain.OrderPaid, domain.OrderFulfilling:
		order, err = r.adminOrderSvc.UpdateOrderStatus(ctx, order.ID, domain.OrderProcessing)
		if err != nil {
			return orderDeliveryError(c, err)
		}
	default:
		if !service.IsDeliveryApprovedStatus(order.Status) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": service.ErrOrderNotApproved.Error(), "code": "order_not_approved"})
		}
	}

	actor := service.AdminActor{UserID: localUserID(c), IPAddress: c.IP()}
	deliveries, err := r.orderDeliverySvc.DeliverApprovedOrder(ctx, actor, order.ID)
	if err != nil {
		return orderDeliveryError(c, err)
	}
	order.Deliveries = deliveries
	return c.JSON(order)
}

func (r *Router) adminListOrderDeliveries(c *fiber.Ctx) error {
	if r.orderDeliverySvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Fitur pengiriman akun belum dikonfigurasi", "code": "delivery_unavailable"})
	}
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	deliveries, err := r.orderDeliverySvc.ListDeliveries(ctx, c.Params("id"))
	if err != nil {
		return orderDeliveryError(c, err)
	}
	return c.JSON(fiber.Map{"deliveries": deliveries})
}

func (r *Router) adminRetryOrderDelivery(c *fiber.Ctx) error {
	if r.orderDeliverySvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Fitur pengiriman akun belum dikonfigurasi", "code": "delivery_unavailable"})
	}
	ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
	defer cancel()

	actor := service.AdminActor{UserID: localUserID(c), IPAddress: c.IP()}
	delivery, err := r.orderDeliverySvc.RetryDelivery(ctx, actor, c.Params("id"), c.Params("deliveryId"))
	if err != nil {
		return orderDeliveryError(c, err)
	}
	return c.JSON(fiber.Map{"delivery": delivery})
}

func orderDeliveryError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, service.ErrNotFound):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Pesanan tidak ditemukan", "code": "not_found"})
	case errors.Is(err, service.ErrDeliveryNotFound):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error(), "code": "not_found"})
	case errors.Is(err, service.ErrOrderNotApproved):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error(), "code": "order_not_approved"})
	case errors.Is(err, service.ErrInvalidTransition):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error(), "code": "invalid_transition"})
	case errors.Is(err, service.ErrInvalidInput):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error(), "code": "invalid_input"})
	default:
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal memproses pengiriman akun"})
	}
}

func localUserID(c *fiber.Ctx) string {
	value, _ := c.Locals("user_id").(string)
	return value
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
		if errors.Is(err, service.ErrUnauthorized) || errors.Is(err, service.ErrInvalidWebhookSignature) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Tanda tangan webhook tidak valid"})
		}
		if errors.Is(err, service.ErrInvalidPaymentEvent) || errors.Is(err, service.ErrInvalidInput) || errors.Is(err, service.ErrPaymentMismatch) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Notifikasi pembayaran tidak valid", "code": "invalid_payment_event",
			})
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
	active, err := parseBoolPtr(c.Query("active"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Filter status voucher tidak valid", "code": "invalid_input"})
	}
	filter := service.VoucherFilter{
		Active:   active,
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
		if errors.Is(err, service.ErrInvalidInput) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if errors.Is(err, service.ErrConflict) {
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
		if errors.Is(err, service.ErrNotFound) {
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
		if errors.Is(err, service.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Voucher tidak ditemukan"})
		}
		if errors.Is(err, service.ErrInvalidInput) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if errors.Is(err, service.ErrConflict) {
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
		if errors.Is(err, service.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Voucher tidak ditemukan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal menghapus voucher"})
	}

	return c.JSON(fiber.Map{"message": "Voucher berhasil dihapus"})
}

func (r *Router) adminListInventoryAccounts(c *fiber.Ctx) error {
	active, err := parseBoolPtr(c.Query("active"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Filter status inventaris tidak valid", "code": "invalid_input"})
	}
	filter := service.InventoryAccountFilter{
		ProductID: c.Query("product_id"),
		Status:    c.Query("status"),
		Active:    active,
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
		if errors.Is(err, service.ErrInvalidInput) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Filter inventaris tidak valid", "code": "invalid_input"})
		}
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
		if errors.Is(err, service.ErrInvalidInput) || errors.Is(err, service.ErrOTPSecretInvalid) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error(), "code": "invalid_input"})
		}
		if errors.Is(err, service.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Produk tidak ditemukan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal menambahkan akun inventaris"})
	}

	account.EncryptedCredentials = nil
	account.OTPSecretEncrypted = nil
	return c.Status(fiber.StatusCreated).JSON(account)
}

func (r *Router) adminCreateInventorySlots(c *fiber.Ctx) error {
	var input service.CreateSlotsInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format data permintaan tidak valid"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	result, err := r.adminInventorySvc.CreateSlots(ctx, input)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidInput):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Jumlah slot harus antara 1 dan 100, dan paket harus dipilih", "code": "invalid_input"})
		case errors.Is(err, service.ErrAccountRequired):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error(), "code": "account_required"})
		case errors.Is(err, service.ErrSharingLimitExceeded):
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error(), "code": "sharing_limit_exceeded"})
		case errors.Is(err, service.ErrInventoryAccountNeedsReview):
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error(), "code": "needs_review"})
		case errors.Is(err, service.ErrInventoryAccountUnavailable):
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error(), "code": "inventory_account_unavailable"})
		case errors.Is(err, service.ErrInventoryAccountMismatch):
			return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error(), "code": "inventory_account_mismatch"})
		case errors.Is(err, service.ErrNotFound):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Paket atau akun inventaris tidak ditemukan"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal menambahkan slot inventaris"})
	}

	return c.Status(fiber.StatusCreated).JSON(result)
}

// adminListInventorySlots returns the read-only slot and assignment view for
// one account. It never exposes credentials.
func (r *Router) adminListInventorySlots(c *fiber.Ctx) error {
	accountID := c.Params("id")
	productPriceID := c.Query("product_price_id")
	status := c.Query("status")
	page := c.QueryInt("page", 1)
	perPage := c.QueryInt("per_page", 50)
	if perPage > 100 {
		perPage = 100
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	slots, total, err := r.adminInventorySvc.ListSlots(ctx, accountID, productPriceID, status, page, perPage)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotFound):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Akun inventaris tidak ditemukan"})
		case errors.Is(err, service.ErrInvalidInput):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Filter slot tidak valid", "code": "invalid_input"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal mengambil daftar slot inventaris"})
	}

	return c.JSON(fiber.Map{"slots": slots, "page": page, "per_page": perPage, "total": total})
}

func (r *Router) adminGetInventoryAccount(c *fiber.Ctx) error {
	id := c.Params("id")

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	account, err := r.adminInventorySvc.GetInventoryAccount(ctx, id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
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
		if errors.Is(err, service.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Akun inventaris tidak ditemukan"})
		}
		if errors.Is(err, service.ErrInvalidInput) || errors.Is(err, service.ErrOTPSecretInvalid) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error(), "code": "invalid_input"})
		}
		// Menurunkan sharing_limit di bawah jumlah slot terprovisi bukan kesalahan
		// server: kontrak W15 meminta 409 dengan kode yang bisa dibaca frontend.
		if errors.Is(err, service.ErrSharingLimitExceeded) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error(), "code": "sharing_limit_exceeded"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal memperbarui akun inventaris"})
	}

	account.EncryptedCredentials = nil
	account.OTPSecretEncrypted = nil
	return c.JSON(account)
}

// adminGenerateInventoryOTP returns the currently valid RFC 6238 code. The
// secret stays encrypted at rest and is never echoed; the response carries only
// the short-lived code and its remaining seconds.
func (r *Router) adminGenerateInventoryOTP(c *fiber.Ctx) error {
	id := c.Params("id")
	actorID := localUserID(c)

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	code, err := r.adminInventorySvc.GenerateOTP(ctx, id, actorID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotFound):
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Akun inventaris tidak ditemukan"})
		case errors.Is(err, service.ErrOTPNotConfigured):
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error(), "code": "otp_not_configured"})
		case errors.Is(err, service.ErrOTPRateLimited):
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": err.Error(), "code": "rate_limited"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Gagal membuat kode OTP"})
	}

	return c.JSON(code)
}

func (r *Router) adminDeleteInventoryAccount(c *fiber.Ctx) error {
	id := c.Params("id")

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	err := r.adminInventorySvc.DeleteInventoryAccount(ctx, id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Akun inventaris tidak ditemukan"})
		}
		if errors.Is(err, service.ErrInventoryAccountInUse) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error(), "code": "inventory_account_in_use"})
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
