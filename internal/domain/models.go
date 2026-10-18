package domain

import "time"

type UserRole string

const (
	RoleCustomer UserRole = "customer"
	RoleAdmin    UserRole = "admin"
	RoleSeller   UserRole = "seller"
)

type User struct {
	ID              string     `json:"id" gorm:"type:uuid;primaryKey"`
	Email           string     `json:"email" gorm:"uniqueIndex:uni_users_email;not null"`
	Name            string     `json:"name" gorm:"not null"`
	PasswordHash    string     `json:"-" gorm:"not null"`
	Role            UserRole   `json:"role" gorm:"type:varchar(24);not null;default:customer"`
	Active          bool       `json:"active" gorm:"not null;default:true;index"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	GoogleSubject   *string    `json:"-" gorm:"uniqueIndex:uni_users_google_subject"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"-" gorm:"index"`
}

type RefreshToken struct {
	ID        string     `json:"id" gorm:"type:uuid;primaryKey"`
	UserID    string     `json:"user_id" gorm:"type:uuid;not null;index"`
	TokenHash string     `json:"-" gorm:"size:64;uniqueIndex;not null"`
	ExpiresAt time.Time  `json:"expires_at" gorm:"not null;index"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	UserAgent string     `json:"-"`
	IPAddress string     `json:"-"`
	CreatedAt time.Time  `json:"created_at"`
}

type EmailVerificationToken struct {
	ID        string     `json:"id" gorm:"type:uuid;primaryKey"`
	UserID    string     `json:"user_id" gorm:"type:uuid;not null;index"`
	TokenHash string     `json:"-" gorm:"size:64;uniqueIndex;not null"`
	ExpiresAt time.Time  `json:"expires_at" gorm:"not null;index"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// PasswordResetToken is a single-use, hashed "lupa kata sandi" token.
type PasswordResetToken struct {
	ID        string     `json:"id" gorm:"type:uuid;primaryKey"`
	UserID    string     `json:"user_id" gorm:"type:uuid;not null;index"`
	TokenHash string     `json:"-" gorm:"size:64;uniqueIndex;not null"`
	ExpiresAt time.Time  `json:"expires_at" gorm:"not null;index"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// IssueReport is a "Laporan Kendala" submission. The order number is stored as
// text rather than a foreign key on purpose: a report outlives the order it
// refers to, and the admin still needs to read it afterwards.
type IssueReport struct {
	ID          string     `json:"id" gorm:"type:uuid;primaryKey"`
	Name        string     `json:"name" gorm:"not null"`
	Email       string     `json:"email" gorm:"not null;index"`
	OrderNumber string     `json:"order_number" gorm:"not null;index"`
	Category    string     `json:"category" gorm:"not null"`
	Message     string     `json:"message" gorm:"not null"`
	Status      string     `json:"status" gorm:"size:24;not null;default:new"`
	IPAddress   string     `json:"-" gorm:"size:45"`
	CreatedAt   time.Time  `json:"created_at" gorm:"index"`
	HandledAt   *time.Time `json:"handled_at,omitempty"`
}

type Category struct {
	ID          string     `json:"id" gorm:"type:uuid;primaryKey"`
	Name        string     `json:"name" gorm:"not null"`
	Slug        string     `json:"slug" gorm:"uniqueIndex;not null"`
	Description string     `json:"description"`
	ImageURL    string     `json:"image_url"`
	Active      bool       `json:"active" gorm:"not null;default:true;index"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeletedAt   *time.Time `json:"-" gorm:"index"`
}

type Product struct {
	ID          string         `json:"id" gorm:"type:uuid;primaryKey"`
	CategoryID  string         `json:"category_id" gorm:"type:uuid;not null;index"`
	Category    *Category      `json:"category,omitempty" gorm:"foreignKey:CategoryID"`
	Name        string         `json:"name" gorm:"not null"`
	Slug        string         `json:"slug" gorm:"uniqueIndex;not null"`
	Description string         `json:"description"`
	ImageURL    string         `json:"image_url"`
	Active      bool           `json:"active" gorm:"not null;default:true;index"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   *time.Time     `json:"-" gorm:"index"`
	Prices      []ProductPrice `json:"prices,omitempty" gorm:"foreignKey:ProductID"`
}

type ProductWithStock struct {
	Product
	Prices []ProductPriceWithStock `json:"prices,omitempty"`
}

type ProductPriceWithStock struct {
	ProductPrice
	AvailableSlots int `json:"available_slots"`
}

type ProductDuration struct {
	ID        string    `json:"id" gorm:"type:uuid;primaryKey"`
	Name      string    `json:"name" gorm:"not null"`
	Days      int       `json:"days" gorm:"not null"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ProductPrice struct {
	ID         string           `json:"id" gorm:"type:uuid;primaryKey"`
	ProductID  string           `json:"product_id" gorm:"type:uuid;not null;index"`
	Product    *Product         `json:"product,omitempty" gorm:"foreignKey:ProductID"`
	DurationID string           `json:"duration_id" gorm:"type:uuid;not null;index"`
	Duration   *ProductDuration `json:"duration,omitempty" gorm:"foreignKey:DurationID"`
	Amount     int64            `json:"amount" gorm:"not null;check:amount >= 0"`
	Currency   string           `json:"currency" gorm:"size:3;not null;default:IDR"`
	Active     bool             `json:"active" gorm:"not null;default:true;index"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
}

type InventoryAccount struct {
	ID                   string     `json:"id" gorm:"type:uuid;primaryKey"`
	ProductID            string     `json:"product_id" gorm:"type:uuid;not null;index"`
	EncryptedCredentials []byte     `json:"-" gorm:"not null"`
	Status               string     `json:"status" gorm:"size:24;not null;default:active;index"`
	ExpiresAt            *time.Time `json:"expires_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type InventorySlot struct {
	ID                 string     `json:"id" gorm:"type:uuid;primaryKey"`
	InventoryAccountID string     `json:"inventory_account_id" gorm:"type:uuid;not null;index"`
	ProductPriceID     string     `json:"product_price_id" gorm:"type:uuid;not null;index"`
	Status             string     `json:"status" gorm:"size:24;not null;default:available;index"`
	ReservedBy         *string    `json:"reserved_by,omitempty" gorm:"type:uuid;index"`
	ReservedUntil      *time.Time `json:"reserved_until,omitempty" gorm:"index"`
	OrderItemID        *string    `json:"order_item_id,omitempty" gorm:"type:uuid;index"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type Cart struct {
	ID        string     `json:"id" gorm:"type:uuid;primaryKey"`
	UserID    string     `json:"user_id" gorm:"type:uuid;not null;index"`
	Items     []CartItem `json:"items,omitempty" gorm:"foreignKey:CartID"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type CartItem struct {
	ID             string        `json:"id" gorm:"type:uuid;primaryKey"`
	CartID         string        `json:"cart_id" gorm:"type:uuid;not null;index"`
	ProductPriceID string        `json:"product_price_id" gorm:"type:uuid;not null;index"`
	ProductPrice   *ProductPrice `json:"product_price,omitempty" gorm:"foreignKey:ProductPriceID"`
	Quantity       int           `json:"quantity" gorm:"not null;check:quantity > 0"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

// CheckoutAttempt is the persisted ledger entry for one owner-scoped
// idempotency key. It is written before any provider call so an interrupted
// request can be reconciled instead of charging twice.
type CheckoutAttempt struct {
	ID                 string    `json:"-" gorm:"type:uuid;primaryKey"`
	UserID             string    `json:"-" gorm:"type:uuid;not null;uniqueIndex:uni_checkout_attempts_user_key"`
	IdempotencyKey     string    `json:"-" gorm:"size:128;not null;uniqueIndex:uni_checkout_attempts_user_key"`
	RequestFingerprint string    `json:"-" gorm:"size:64;not null"`
	State              string    `json:"-" gorm:"size:24;not null;default:started"`
	OrderID            *string   `json:"-" gorm:"type:uuid"`
	Provider           *string   `json:"-" gorm:"size:32"`
	ProviderPaymentID  *string   `json:"-" gorm:"size:255"`
	CheckoutURL        *string   `json:"-"`
	ErrorCode          *string   `json:"-" gorm:"size:64"`
	CreatedAt          time.Time `json:"-"`
	UpdatedAt          time.Time `json:"-"`
}

type OrderStatus string

const (
	OrderPending    OrderStatus = "pending_payment"
	OrderPaid       OrderStatus = "paid"
	OrderFulfilling OrderStatus = "fulfilling"
	OrderProcessing OrderStatus = "processing"
	OrderShipped    OrderStatus = "shipped"
	OrderDelivered  OrderStatus = "delivered"
	OrderCompleted  OrderStatus = "completed"
	OrderCancelled  OrderStatus = "cancelled"
	OrderExpired    OrderStatus = "expired"
	OrderRefunded   OrderStatus = "refunded"
)

type Order struct {
	ID              string      `json:"id" gorm:"type:uuid;primaryKey"`
	Number          string      `json:"number" gorm:"uniqueIndex;not null"`
	UserID          string      `json:"user_id" gorm:"type:uuid;not null;index"`
	VoucherID       *string     `json:"voucher_id,omitempty" gorm:"type:uuid;index"`
	Status          OrderStatus `json:"status" gorm:"size:32;not null;index"`
	Subtotal        int64       `json:"subtotal" gorm:"not null"`
	Discount        int64       `json:"discount" gorm:"not null;default:0"`
	Total           int64       `json:"total" gorm:"not null"`
	Currency        string      `json:"currency" gorm:"size:3;not null;default:IDR"`
	PaymentDueAt    time.Time   `json:"payment_due_at"`
	ShippingAddress string      `json:"shipping_address"`
	Notes           string      `json:"notes"`
	IdempotencyKey  string      `json:"-" gorm:"size:128;not null"`
	// RequestFingerprint is the canonical hash that makes a replay safe. Older
	// rows created before migration 010 keep NULL and are rejected as
	// idempotency conflicts rather than guessed.
	RequestFingerprint *string     `json:"-" gorm:"size:64"`
	Items              []OrderItem `json:"items,omitempty" gorm:"foreignKey:OrderID"`
	Payments           []Payment   `json:"payments,omitempty" gorm:"foreignKey:OrderID"`
	// Customer backs the admin order list and dashboard, both of which preload
	// it. Reads that do not preload simply omit the field.
	Customer  *User     `json:"customer,omitempty" gorm:"foreignKey:UserID"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type OrderItem struct {
	ID             string `json:"id" gorm:"type:uuid;primaryKey"`
	OrderID        string `json:"order_id" gorm:"type:uuid;not null;index"`
	ProductPriceID string `json:"product_price_id" gorm:"type:uuid;not null;index"`
	ProductName    string `json:"product_name" gorm:"not null"`
	DurationName   string `json:"duration_name" gorm:"not null"`
	UnitPrice      int64  `json:"unit_price" gorm:"not null"`
	Quantity       int    `json:"quantity" gorm:"not null"`
	DeviceCount    int    `json:"device_count" gorm:"not null;default:1"`
	LineTotal      int64  `json:"line_total" gorm:"not null"`
}

type Payment struct {
	ID                string     `json:"id" gorm:"type:uuid;primaryKey"`
	OrderID           string     `json:"order_id" gorm:"type:uuid;not null;index"`
	Provider          string     `json:"provider" gorm:"size:32;not null"`
	ProviderPaymentID string     `json:"provider_payment_id" gorm:"uniqueIndex;not null"`
	Status            string     `json:"status" gorm:"size:32;not null;index"`
	Amount            int64      `json:"amount" gorm:"not null"`
	CheckoutURL       string     `json:"checkout_url,omitempty"`
	PaidAt            *time.Time `json:"paid_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type VoucherType string

const (
	VoucherFixed      VoucherType = "fixed"
	VoucherPercentage VoucherType = "percentage"
)

type Voucher struct {
	ID           string      `json:"id" gorm:"type:uuid;primaryKey"`
	Code         string      `json:"code" gorm:"uniqueIndex;not null"`
	Type         VoucherType `json:"type" gorm:"size:16;not null"`
	Value        int64       `json:"value" gorm:"not null"`
	MinOrder     int64       `json:"min_order" gorm:"not null;default:0"`
	MaxDiscount  int64       `json:"max_discount" gorm:"not null;default:0"`
	UsageLimit   int         `json:"usage_limit" gorm:"not null;default:0"`
	PerUserLimit int         `json:"per_user_limit" gorm:"not null;default:0"`
	StartsAt     time.Time   `json:"starts_at" gorm:"not null"`
	EndsAt       time.Time   `json:"ends_at" gorm:"not null"`
	Active       bool        `json:"active" gorm:"not null;default:true;index"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

type VoucherRedemption struct {
	ID        string    `json:"id" gorm:"type:uuid;primaryKey"`
	VoucherID string    `json:"voucher_id" gorm:"type:uuid;not null;index:idx_voucher_user_order"`
	UserID    string    `json:"user_id" gorm:"type:uuid;not null;index:idx_voucher_user_order"`
	OrderID   string    `json:"order_id" gorm:"type:uuid;not null;uniqueIndex"`
	Amount    int64     `json:"amount" gorm:"not null"`
	CreatedAt time.Time `json:"created_at"`
}

type Notification struct {
	ID        string     `json:"id" gorm:"type:uuid;primaryKey"`
	UserID    string     `json:"user_id" gorm:"type:uuid;not null;index"`
	Type      string     `json:"type" gorm:"size:32;not null"`
	Title     string     `json:"title" gorm:"not null"`
	Body      string     `json:"body" gorm:"not null"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type AuditLog struct {
	ID           string  `json:"id" gorm:"type:uuid;primaryKey"`
	ActorUserID  *string `json:"actor_user_id,omitempty" gorm:"type:uuid;index"`
	Action       string  `json:"action" gorm:"size:96;not null;index"`
	ResourceType string  `json:"resource_type" gorm:"size:64;not null"`
	ResourceID   string  `json:"resource_id" gorm:"index"`
	IPAddress    string  `json:"ip_address"`
	Metadata     []byte  `json:"metadata" gorm:"type:jsonb"`
	// ActorUser is what ListAuditLogs preloads; without the association declared
	// GORM answers ErrUnsupportedRelation and the audit-log query fails.
	ActorUser *User     `json:"actor_user,omitempty" gorm:"foreignKey:ActorUserID"`
	CreatedAt time.Time `json:"created_at" gorm:"index"`
}

type WebhookEvent struct {
	ID              string    `json:"id" gorm:"type:uuid;primaryKey"`
	Provider        string    `json:"provider" gorm:"size:32;not null;uniqueIndex:idx_provider_event"`
	ProviderEventID string    `json:"provider_event_id" gorm:"not null;uniqueIndex:idx_provider_event"`
	PayloadHash     string    `json:"payload_hash" gorm:"size:64;not null"`
	ProcessedAt     time.Time `json:"processed_at"`
	CreatedAt       time.Time `json:"created_at"`
}

type Testimonial struct {
	ID          string     `json:"id" gorm:"type:uuid;primaryKey"`
	UserID      string     `json:"user_id" gorm:"type:uuid;not null;index"`
	ProductID   *string    `json:"product_id,omitempty" gorm:"type:uuid;index"`
	Title       string     `json:"title" gorm:"not null"`
	Body        string     `json:"body" gorm:"not null"`
	Rating      int        `json:"rating" gorm:"not null;check:rating BETWEEN 1 AND 5"`
	Status      string     `json:"status" gorm:"size:24;not null;default:pending;index"`
	ModeratorID *string    `json:"moderator_id,omitempty" gorm:"type:uuid;index"`
	ModeratedAt *time.Time `json:"moderated_at,omitempty"`
	// Reviewer and Product are the associations the repository preloads. The
	// foreign keys are spelled out because two columns (user_id and
	// moderator_id) point at users, so GORM cannot guess which one User means.
	Reviewer  *User     `json:"reviewer,omitempty" gorm:"foreignKey:UserID"`
	Product   *Product  `json:"product,omitempty" gorm:"foreignKey:ProductID"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PublicTestimonial is the only shape the storefront ever sees. It deliberately
// drops the user id, the moderator columns and the reviewer's email address:
// approval by an admin is consent to publish the review, not the customer's
// account details.
type PublicTestimonial struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Body         string    `json:"body"`
	Rating       int       `json:"rating"`
	ReviewerName string    `json:"reviewer_name"`
	ProductName  string    `json:"product_name,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type Asset struct {
	ID               string     `json:"id" gorm:"type:uuid;primaryKey"`
	UserID           string     `json:"user_id" gorm:"type:uuid;not null;index"`
	ObjectKey        string     `json:"object_key" gorm:"not null"`
	Filename         string     `json:"filename" gorm:"not null"`
	MimeType         string     `json:"mime_type" gorm:"size:128;not null"`
	SizeBytes        int64      `json:"size_bytes" gorm:"not null;check:size_bytes >= 0"`
	Status           string     `json:"status" gorm:"size:24;not null;default:pending;index"`
	PresignURL       string     `json:"presign_url,omitempty"`
	PresignExpiresAt *time.Time `json:"presign_expires_at,omitempty"`
	UploadedAt       *time.Time `json:"uploaded_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}
