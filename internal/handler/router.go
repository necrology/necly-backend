package handler

import (
	"context"
	"fmt"
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
	tokens            *service.TokenManager
	cipher            *service.CredentialCipher
	redis             *redis.Client
}

func NewRouter(authSvc *service.AuthService, catalogSvc *service.CatalogService, cartSvc *service.CartService, checkoutSvc *service.CheckoutService, webhookSvc *service.WebhookService, orderSvc *service.OrderService, adminSvc *service.AdminDashboardService, adminUserSvc *service.AdminUserService, adminProductSvc *service.AdminProductService, adminCategorySvc *service.AdminCategoryService, adminOrderSvc *service.AdminOrderService, adminVoucherSvc *service.AdminVoucherService, adminInventorySvc *service.AdminInventoryService, tokens *service.TokenManager, cipher *service.CredentialCipher, redis *redis.Client) *Router {
	return &Router{authSvc: authSvc, catalogSvc: catalogSvc, cartSvc: cartSvc, checkoutSvc: checkoutSvc, webhookSvc: webhookSvc, orderSvc: orderSvc, adminSvc: adminSvc, adminUserSvc: adminUserSvc, adminProductSvc: adminProductSvc, adminCategorySvc: adminCategorySvc, adminOrderSvc: adminOrderSvc, adminVoucherSvc: adminVoucherSvc, adminInventorySvc: adminInventorySvc, tokens: tokens, cipher: cipher, redis: redis}
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

		// Check Redis if configured
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
	public := api.Group("/public")
	public.Get("/categories", r.listCategories)
	public.Get("/categories/:id", r.getCategory)
	public.Get("/products", r.listProducts)
	public.Get("/products/:id", r.getProduct)
	public.Get("/products/:id/prices", r.listProductPrices)
	// Provider webhook callbacks - public, no auth required
	public.Post("/webhooks/:provider", r.receiveWebhook)
}

func (r *Router) registerAuth(api fiber.Router) {
	auth := api.Group("/auth")
	auth.Post("/register", r.register)
	auth.Post("/login", r.login)
	auth.Post("/refresh", r.refresh)
	auth.Post("/logout", r.logout)
	auth.Post("/verify-email", r.verifyEmail)
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
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing authorization header"})
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid authorization format"})
		}

		claims, err := r.tokens.ParseAccess(parts[1])
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid or expired token"})
		}

		c.Locals("user_id", claims.Subject)
		c.Locals("user_role", claims.Role)
		return c.Next()
	}
}

func (r *Router) requireRole(requiredRole domain.UserRole) fiber.Handler {
	return func(c *fiber.Ctx) error {
		role := c.Locals("user_role")
		if role != requiredRole {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "insufficient permissions"})
		}
		return c.Next()
	}
}

func (r *Router) register(c *fiber.Ctx) error {
	var input service.RegisterInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	result, err := r.authSvc.Register(ctx, input)
	if err != nil {
		if strings.Contains(err.Error(), "conflict") || strings.Contains(err.Error(), "duplicate") {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "email already registered"})
		}
		if strings.Contains(err.Error(), "invalid") {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "registration failed"})
	}

	return c.Status(fiber.StatusCreated).JSON(result)
}

func (r *Router) login(c *fiber.Ctx) error {
	var input service.LoginInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
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
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "login failed"})
	}

	c.Cookie(&fiber.Cookie{
		Name:     "refresh_token",
		Value:    session.RefreshToken,
		HTTPOnly: true,
		Secure:   c.Protocol() == "https",
		SameSite: "Lax",
		MaxAge:   int(7 * 24 * time.Hour / time.Second),
		Path:     "/",
	})

	return c.JSON(fiber.Map{
		"user":         session.User,
		"access_token": session.AccessToken,
		"expires_in":   session.ExpiresIn,
	})
}

func (r *Router) refresh(c *fiber.Ctx) error {
	refreshToken := c.Cookies("refresh_token")
	if refreshToken == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing refresh token"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	session, err := r.authSvc.Refresh(ctx, refreshToken, service.ClientInfo{
		IPAddress: c.IP(),
		UserAgent: c.Get("User-Agent"),
	})
	if err != nil {
		c.ClearCookie("refresh_token")
		if err == service.ErrUnauthorized {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "refresh token revoked or expired"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "refresh failed"})
	}

	c.Cookie(&fiber.Cookie{
		Name:     "refresh_token",
		Value:    session.RefreshToken,
		HTTPOnly: true,
		Secure:   c.Protocol() == "https",
		SameSite: "Lax",
		MaxAge:   int(7 * 24 * time.Hour / time.Second),
		Path:     "/",
	})

	return c.JSON(fiber.Map{
		"user":         session.User,
		"access_token": session.AccessToken,
		"expires_in":   session.ExpiresIn,
	})
}

func (r *Router) logout(c *fiber.Ctx) error {
	refreshToken := c.Cookies("refresh_token")
	if refreshToken != "" {
		ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
		defer cancel()
		_ = r.authSvc.Logout(ctx, refreshToken)
	}
	c.ClearCookie("refresh_token")
	return c.JSON(fiber.Map{"message": "logged out"})
}

func (r *Router) verifyEmail(c *fiber.Ctx) error {
	var input struct {
		Token string `json:"token"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	user, err := r.authSvc.VerifyEmail(ctx, input.Token)
	if err != nil {
		if err == service.ErrUnauthorized || err == service.ErrNotFound {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid or expired verification token"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "verification failed"})
	}

	return c.JSON(fiber.Map{"message": "email verified", "user": user})
}

func (r *Router) googleLogin(c *fiber.Ctx) error {
	if r.authSvc.GoogleConfig() == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "google oauth not configured"})
	}
	cfg := r.authSvc.GoogleConfig()
	state, err := r.authSvc.GenerateOAuthState()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate oauth state"})
	}
	// Store state in Redis with TTL
	if r.redis != nil {
		ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
		defer cancel()
		if err := r.redis.Set(ctx, "oauth:state:"+state, c.IP(), cfg.StateTTL).Err(); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to store oauth state"})
		}
	}
	authURL := fmt.Sprintf("%s?client_id=%s&redirect_uri=%s&response_type=code&scope=openid%%20email%%20profile&access_type=offline&prompt=consent&state=%s",
		cfg.AuthURL, url.QueryEscape(cfg.ClientID), url.QueryEscape(cfg.RedirectURL), state)
	return c.Redirect(authURL, fiber.StatusTemporaryRedirect)
}

func (r *Router) googleCallback(c *fiber.Ctx) error {
	if r.authSvc.GoogleConfig() == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "google oauth not configured"})
	}
	cfg := r.authSvc.GoogleConfig()
	code := c.Query("code")
	state := c.Query("state")
	if code == "" || state == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing code or state"})
	}
	// Verify state
	if r.redis != nil {
		ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
		defer cancel()
		storedIP, err := r.redis.Get(ctx, "oauth:state:"+state).Result()
		if err != nil || storedIP != c.IP() {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid or expired oauth state"})
		}
		r.redis.Del(ctx, "oauth:state:"+state)
	}
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()
	session, err := r.authSvc.ExchangeGoogleCode(ctx, code, cfg)
	if err != nil {
		if err == service.ErrUnauthorized {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid google code"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "google oauth exchange failed"})
	}
	// Redirect to frontend with tokens
	returnUrl := fmt.Sprintf("%s?access_token=%s&refresh_token=%s&expires_in=%d", cfg.FrontendReturnURL, session.AccessToken, session.RefreshToken, session.ExpiresIn)
	return c.Redirect(returnUrl, fiber.StatusTemporaryRedirect)
}

func (r *Router) listCategories(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	defer cancel()
	categories, _, err := r.catalogSvc.ListCategories(ctx, true, 1, 100)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list categories"})
	}
	return c.JSON(fiber.Map{"categories": categories})
}

func (r *Router) getCategory(c *fiber.Ctx) error {
	id := c.Params("id")
	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	defer cancel()
	cat, err := r.catalogSvc.GetCategory(ctx, id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "category not found"})
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
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list products"})
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
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "product not found"})
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
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list prices"})
	}
	return c.JSON(fiber.Map{"prices": prices})
}

func (r *Router) getMe(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	defer cancel()
	user, err := r.authSvc.GetUserByID(ctx, userID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "user not found"})
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	input.ProductPriceID = strings.TrimSpace(input.ProductPriceID)
	if input.ProductPriceID == "" || input.Quantity <= 0 || input.Quantity > 100 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "product_price_id and quantity (1-100) are required"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	item, err := r.cartSvc.AddItem(ctx, userID, input.ProductPriceID, input.Quantity)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "product price not found"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to add to cart"})
	}

	return c.Status(fiber.StatusCreated).JSON(item)
}

func (r *Router) getCart(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	cart, err := r.cartSvc.GetCartWithValidation(ctx, userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to get cart"})
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if input.Quantity <= 0 || input.Quantity > 100 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "quantity must be between 1 and 100"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	err := r.cartSvc.UpdateItem(ctx, userID, itemID, input.Quantity)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "cart item not found"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update cart item"})
	}

	return c.JSON(fiber.Map{"message": "cart item updated"})
}

func (r *Router) removeCartItem(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	itemID := c.Params("itemId")

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	err := r.cartSvc.RemoveItem(ctx, userID, itemID)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "cart item not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to remove cart item"})
	}

	return c.JSON(fiber.Map{"message": "cart item removed"})
}

func (r *Router) checkout(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var input service.CheckoutInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
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
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "product or voucher not found"})
		}
		if err == service.ErrOutOfStock {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "insufficient stock"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "checkout failed"})
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
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list orders"})
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
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "order not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to get order"})
	}

	return c.JSON(order)
}

func (r *Router) dashboard(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()

	stats, err := r.adminSvc.GetDashboardStats(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to get dashboard stats"})
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
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list users"})
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
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "user not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to get user"})
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
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
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "user not found"})
		}
		if err == service.ErrConflict {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "email already in use"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update user"})
	}

	if role != nil {
		user, err = r.adminUserSvc.UpdateUserRole(ctx, id, *role)
		if err != nil {
			if err == service.ErrInvalidInput {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid role"})
			}
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update user role"})
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
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "user not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete user"})
	}

	return c.JSON(fiber.Map{"message": "user deleted"})
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
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list products"})
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	product, err := r.adminProductSvc.CreateProduct(ctx, input.CategoryID, input.Name, input.Slug, input.Description, input.ImageURL)
	if err != nil {
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if err == service.ErrConflict {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "product SKU already exists"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create product"})
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	product, err := r.adminProductSvc.UpdateProduct(ctx, id, input.CategoryID, input.Name, input.Slug, input.Description, input.ImageURL, input.Active)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "product not found"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if err == service.ErrConflict {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "product SKU already exists"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update product"})
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
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "product not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete product"})
	}

	return c.JSON(fiber.Map{"message": "product deleted"})
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
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list categories"})
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	category, err := r.adminCategorySvc.CreateCategory(ctx, input.Name, input.Slug, input.Description, input.ImageURL)
	if err != nil {
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if err == service.ErrConflict {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "category slug already exists"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create category"})
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	category, err := r.adminCategorySvc.UpdateCategory(ctx, id, input.Name, input.Slug, input.Description, input.ImageURL, input.Active)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "category not found"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if err == service.ErrConflict {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "category slug already exists"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update category"})
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
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "category not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete category"})
	}

	return c.JSON(fiber.Map{"message": "category deleted"})
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
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list orders"})
	}

	return c.JSON(fiber.Map{"orders": orders, "page": page, "per_page": perPage, "total": total})
}

func (r *Router) adminUpdateOrderStatus(c *fiber.Ctx) error {
	id := c.Params("id")

	var input struct {
		Status string `json:"status"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if input.Status == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "status is required"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	order, err := r.adminOrderSvc.UpdateOrderStatus(ctx, id, domain.OrderStatus(input.Status))
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "order not found"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update order status"})
	}

	return c.JSON(order)
}

func (r *Router) receiveWebhook(c *fiber.Ctx) error {
	provider := c.Params("provider")
	if provider == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "provider is required"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
	defer cancel()

	payload := c.Body()
	headers := make(map[string]string)
	c.Request().Header.VisitAll(func(key, value []byte) {
		headers[string(key)] = string(value)
	})

	result, err := r.webhookSvc.Process(ctx, headers, payload)
	if err != nil {
		if err == service.ErrUnauthorized {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid webhook signature"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "webhook processing failed"})
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
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list audit logs"})
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
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list vouchers"})
	}

	return c.JSON(fiber.Map{"vouchers": vouchers, "page": page, "per_page": perPage, "total": total})
}

func (r *Router) adminCreateVoucher(c *fiber.Ctx) error {
	var input service.CreateVoucherInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	voucher, err := r.adminVoucherSvc.CreateVoucher(ctx, input)
	if err != nil {
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if err == service.ErrConflict {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "voucher code already exists"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create voucher"})
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
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "voucher not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to get voucher"})
	}

	return c.JSON(voucher)
}

func (r *Router) adminUpdateVoucher(c *fiber.Ctx) error {
	id := c.Params("id")

	var input service.UpdateVoucherInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	voucher, err := r.adminVoucherSvc.UpdateVoucher(ctx, id, input)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "voucher not found"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if err == service.ErrConflict {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "voucher code already exists"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update voucher"})
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
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "voucher not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete voucher"})
	}

	return c.JSON(fiber.Map{"message": "voucher deleted"})
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
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list inventory accounts"})
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	account, err := r.adminInventorySvc.CreateInventoryAccount(ctx, input)
	if err != nil {
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create inventory account"})
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
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "inventory account not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to get inventory account"})
	}

	account.EncryptedCredentials = nil
	return c.JSON(account)
}

func (r *Router) adminUpdateInventoryAccount(c *fiber.Ctx) error {
	id := c.Params("id")

	var input service.UpdateInventoryAccountInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	account, err := r.adminInventorySvc.UpdateInventoryAccount(ctx, id, input)
	if err != nil {
		if err == service.ErrNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "inventory account not found"})
		}
		if err == service.ErrInvalidInput {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update inventory account"})
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
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "inventory account not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete inventory account"})
	}

	return c.JSON(fiber.Map{"message": "inventory account deleted"})
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
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list payments"})
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
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list notifications"})
	}

	return c.JSON(fiber.Map{"notifications": logs, "page": page, "per_page": perPage, "total": total})
}
