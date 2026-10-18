package gormrepo

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/migrate"
	"github.com/necly/services/internal/platform/testdb"
	"github.com/necly/services/internal/service"
	"gorm.io/gorm"
)

func openW7DB(t *testing.T) *gorm.DB {
	t.Helper()
	if !testdb.Available() {
		t.Skip("DB_HOST/DB_USER not set; skipping PostgreSQL integration test")
	}
	db, cleanup, err := testdb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Run(context.Background(), db, migrate.Options{Dir: dir}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

type w7CatalogFixture struct {
	db       *gorm.DB
	store    *Store
	duration string
	prices   map[string]string
	products map[string]string
}

// newW7CatalogFixture mirrors the W0 canonical fixtures: two categories with
// two products each, one inactive price, durations in different orders and a
// product without sellable slots.
func newW7CatalogFixture(t *testing.T) *w7CatalogFixture {
	t.Helper()
	db := openW7DB(t)
	now := time.Now().UTC()

	video := "0192f0a2-0000-0000-0000-000000000001"
	music := "0192f0a2-0000-0000-0000-000000000002"
	productivity := "0192f0a2-0000-0000-0000-000000000003"
	cloud := "0192f0a2-0000-0000-0000-000000000004"
	categories := []domain.Category{
		{ID: video, Name: "Streaming Video", Slug: "video-streaming", Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: music, Name: "Streaming Musik", Slug: "music-streaming", Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: productivity, Name: "Produktivitas Bersama", Slug: "productivity-suites", Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: cloud, Name: "Penyimpanan Awan", Slug: "cloud-storage", Active: true, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&categories).Error; err != nil {
		t.Fatalf("seed categories: %v", err)
	}

	monthly := "0192f0a1-0000-0000-0000-000000000001"
	annual := "0192f0a1-0000-0000-0000-000000000002"
	durations := []domain.ProductDuration{
		{ID: monthly, Name: "1 Bulan", Days: 30, CreatedAt: now, UpdatedAt: now},
		{ID: annual, Name: "12 Bulan", Days: 365, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&durations).Error; err != nil {
		t.Fatalf("seed durations: %v", err)
	}

	products := map[string]string{
		"netflix":   "0192f0a3-0000-0000-0000-000000000001",
		"disney":    "0192f0a3-0000-0000-0000-000000000002",
		"spotify":   "0192f0a3-0000-0000-0000-000000000003",
		"apple":     "0192f0a3-0000-0000-0000-000000000004",
		"microsoft": "0192f0a3-0000-0000-0000-000000000005",
		"canva":     "0192f0a3-0000-0000-0000-000000000006",
	}
	rows := []domain.Product{
		{ID: products["netflix"], CategoryID: video, Name: "Netflix Premium", Slug: "netflix-premium", Active: true, Labels: domain.StringArray{"Best Seller"}, DeviceOptions: domain.IntArray{1, 2}, CreatedAt: now, UpdatedAt: now},
		{ID: products["disney"], CategoryID: video, Name: "Disney+ Standard", Slug: "disney-standard", Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: products["spotify"], CategoryID: music, Name: "Spotify Premium", Slug: "spotify-premium", Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: products["apple"], CategoryID: music, Name: "Apple Music Family", Slug: "apple-music-family", Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: products["microsoft"], CategoryID: productivity, Name: "Microsoft 365 Family", Slug: "microsoft-365-family", Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: products["canva"], CategoryID: productivity, Name: "Canva Teams", Slug: "canva-teams", Active: false, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed products: %v", err)
	}
	// The columns default to true, so GORM would replace an explicit false on
	// Create. Force the inactive fixture through an UPDATE like the seed SQL does.
	if err := db.Model(&domain.Product{}).Where("id = ?", products["canva"]).Update("active", false).Error; err != nil {
		t.Fatalf("mark canva inactive: %v", err)
	}

	prices := map[string]string{
		"netflix-monthly":  "0192f0a4-0000-0000-0000-000000000001",
		"netflix-annual":   "0192f0a4-0000-0000-0000-000000000002",
		"netflix-inactive": "0192f0a4-0000-0000-0000-000000000019",
		"disney":           "0192f0a4-0000-0000-0000-000000000011",
		"spotify":          "0192f0a4-0000-0000-0000-000000000012",
		"apple":            "0192f0a4-0000-0000-0000-000000000013",
		"microsoft":        "0192f0a4-0000-0000-0000-000000000014",
	}
	priceRows := []domain.ProductPrice{
		{ID: prices["netflix-monthly"], ProductID: products["netflix"], DurationID: monthly, Amount: 186000, Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: prices["netflix-annual"], ProductID: products["netflix"], DurationID: annual, Amount: 250000, Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: prices["netflix-inactive"], ProductID: products["netflix"], DurationID: monthly, Amount: 50000, Currency: "IDR", Active: false, CreatedAt: now, UpdatedAt: now},
		{ID: prices["disney"], ProductID: products["disney"], DurationID: monthly, Amount: 60000, Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: prices["spotify"], ProductID: products["spotify"], DurationID: monthly, Amount: 59000, Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: prices["apple"], ProductID: products["apple"], DurationID: annual, Amount: 250000, Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: prices["microsoft"], ProductID: products["microsoft"], DurationID: annual, Amount: 150000, Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&priceRows).Error; err != nil {
		t.Fatalf("seed prices: %v", err)
	}
	if err := db.Model(&domain.ProductPrice{}).Where("id = ?", prices["netflix-inactive"]).Update("active", false).Error; err != nil {
		t.Fatalf("mark netflix inactive price: %v", err)
	}

	accountIDs := map[string]string{}
	slotRows := []domain.InventorySlot{}
	for _, name := range []string{"netflix", "disney", "spotify", "apple"} {
		accountID := uuid.NewString()
		accountIDs[name] = accountID
		if err := db.Create(&domain.InventoryAccount{
			ID: accountID, ProductID: products[name], EncryptedCredentials: []byte("fixture"),
			Status: "active", CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatalf("seed account %s: %v", name, err)
		}
	}
	addSlot := func(account, price, status string) {
		slotRows = append(slotRows, domain.InventorySlot{
			ID: uuid.NewString(), InventoryAccountID: accountIDs[account], ProductPriceID: price,
			Status: status, CreatedAt: now, UpdatedAt: now,
		})
	}
	addSlot("netflix", prices["netflix-monthly"], "available")
	addSlot("netflix", prices["netflix-monthly"], "reserved")
	addSlot("netflix", prices["netflix-inactive"], "available")
	addSlot("disney", prices["disney"], "available")
	addSlot("spotify", prices["spotify"], "available")
	addSlot("apple", prices["apple"], "available")
	if err := db.Create(&slotRows).Error; err != nil {
		t.Fatalf("seed slots: %v", err)
	}

	return &w7CatalogFixture{
		db: db, store: NewStore(db), duration: monthly,
		prices: prices, products: products,
	}
}

func productIDs(products []domain.ProductWithStock) []string {
	ids := make([]string, len(products))
	for index, product := range products {
		ids[index] = product.ID
	}
	return ids
}

func TestW7PublicCatalogFilterContract(t *testing.T) {
	fixture := newW7CatalogFixture(t)
	ctx := context.Background()
	active := true
	video := "0192f0a2-0000-0000-0000-000000000001"
	music := "0192f0a2-0000-0000-0000-000000000002"
	random := uuid.NewString()

	products, total, err := fixture.store.ListProductsWithStock(ctx, service.ProductFilter{Active: &active, CategoryID: video}, 1, 20)
	if err != nil {
		t.Fatalf("filter video: %v", err)
	}
	if total != 2 {
		t.Fatalf("video total = %d, want 2", total)
	}
	if got := productIDs(products); len(got) != 2 || got[0] != fixture.products["netflix"] || got[1] != fixture.products["disney"] {
		t.Fatalf("video products = %v, want netflix+disney", got)
	}

	products, total, err = fixture.store.ListProductsWithStock(ctx, service.ProductFilter{Active: &active, CategoryID: "0192f0a2-0000-0000-0000-000000000003"}, 1, 20)
	if err != nil {
		t.Fatalf("filter productivity: %v", err)
	}
	if got := productIDs(products); total != 1 || len(got) != 1 || got[0] != fixture.products["microsoft"] {
		t.Fatalf("productivity products = %d/%v, want microsoft only", total, got)
	}

	products, total, err = fixture.store.ListProductsWithStock(ctx, service.ProductFilter{Active: &active, CategoryID: "0192f0a2-0000-0000-0000-000000000004"}, 1, 20)
	if err != nil {
		t.Fatalf("filter cloud: %v", err)
	}
	if total != 0 || len(products) != 0 {
		t.Fatalf("cloud products = %d/%d, want empty", total, len(products))
	}

	products, total, err = fixture.store.ListProductsWithStock(ctx, service.ProductFilter{Active: &active, CategoryID: random}, 1, 20)
	if err != nil {
		t.Fatalf("unknown category: %v", err)
	}
	if total != 0 || len(products) != 0 {
		t.Fatalf("unknown category returned %d products, want 0", total)
	}

	// The public list must contain only active prices and count only their
	// available slots (the reserved and inactive-price slots do not count).
	products, _, err = fixture.store.ListProductsWithStock(ctx, service.ProductFilter{Active: &active, CategoryID: video}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	var netflix *domain.ProductWithStock
	for index := range products {
		if products[index].ID == fixture.products["netflix"] {
			netflix = &products[index]
		}
	}
	if netflix == nil {
		t.Fatal("netflix missing from video category")
	}
	if len(netflix.Prices) != 2 {
		t.Fatalf("netflix prices = %d, want 2 active prices", len(netflix.Prices))
	}
	for _, price := range netflix.Prices {
		if price.ID == fixture.prices["netflix-inactive"] || !price.Active {
			t.Fatalf("inactive price leaked into public response: %#v", price)
		}
	}
	if netflix.Prices[0].ID != fixture.prices["netflix-monthly"] || netflix.Prices[0].AvailableSlots != 1 {
		t.Fatalf("first price = %#v, want monthly with 1 available slot", netflix.Prices[0])
	}
	if netflix.Prices[1].ID != fixture.prices["netflix-annual"] || netflix.Prices[1].AvailableSlots != 0 {
		t.Fatalf("second price = %#v, want annual with 0 available slots", netflix.Prices[1])
	}

	minPrice := int64(150000)
	products, _, err = fixture.store.ListProductsWithStock(ctx, service.ProductFilter{Active: &active, CategoryID: video, MinPrice: &minPrice}, 1, 20)
	if err != nil {
		t.Fatalf("min_price: %v", err)
	}
	if got := productIDs(products); len(got) != 1 || got[0] != fixture.products["netflix"] {
		t.Fatalf("min_price products = %v, want netflix", got)
	}

	maxPrice := int64(70000)
	products, _, err = fixture.store.ListProductsWithStock(ctx, service.ProductFilter{Active: &active, CategoryID: video, MaxPrice: &maxPrice}, 1, 20)
	if err != nil {
		t.Fatalf("max_price: %v", err)
	}
	if got := productIDs(products); len(got) != 1 || got[0] != fixture.products["disney"] {
		t.Fatalf("max_price products = %v, want disney", got)
	}

	products, _, err = fixture.store.ListProductsWithStock(ctx, service.ProductFilter{Active: &active, CategoryID: music, MinPrice: &minPrice}, 1, 20)
	if err != nil {
		t.Fatalf("music min_price: %v", err)
	}
	if got := productIDs(products); len(got) != 1 || got[0] != fixture.products["apple"] {
		t.Fatalf("music min_price products = %v, want apple", got)
	}

	// Both bounds must match the same active price: Netflix has 186000 and
	// 250000, neither inside 200000..240000.
	combinedMin := int64(200000)
	combinedMax := int64(240000)
	products, total, err = fixture.store.ListProductsWithStock(ctx, service.ProductFilter{
		Active: &active, CategoryID: video, MinPrice: &combinedMin, MaxPrice: &combinedMax,
	}, 1, 20)
	if err != nil {
		t.Fatalf("combined price range: %v", err)
	}
	if total != 0 || len(products) != 0 {
		t.Fatalf("combined price range returned %d/%v, want empty", total, productIDs(products))
	}

	products, total, err = fixture.store.ListProductsWithStock(ctx, service.ProductFilter{Active: &active, InStock: true}, 1, 20)
	if err != nil {
		t.Fatalf("in_stock: %v", err)
	}
	for _, product := range products {
		if product.ID == fixture.products["microsoft"] {
			t.Fatal("in_stock=true returned a product without available slots")
		}
	}
	if total != 4 {
		t.Fatalf("in_stock total = %d, want 4", total)
	}

	// The service validates the identifier before the repository sees it, so a
	// category label cannot reach the UUID column.
	catalog := service.NewCatalogService(fixture.store)
	if _, _, err := catalog.ListProductsWithStock(ctx, service.ProductFilter{Active: &active, CategoryID: "Streaming"}, 1, 20); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("display-name category error = %v, want ErrInvalidInput", err)
	}

	detail, err := fixture.store.GetProductWithStock(ctx, fixture.products["netflix"])
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if len(detail.Prices) != 2 || detail.Prices[0].ID != fixture.prices["netflix-monthly"] {
		t.Fatalf("detail prices = %#v", detail.Prices)
	}

	// The public detail route must apply the same active-only rule as the list.
	if _, err := fixture.store.GetProductWithStock(ctx, fixture.products["canva"]); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("inactive product detail error = %v, want ErrNotFound", err)
	}
	if _, err := fixture.store.GetProductBySlugWithStock(ctx, "canva-teams"); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("inactive product slug error = %v, want ErrNotFound", err)
	}
}

func TestW7AdminProductCRUDUsesSharedErrors(t *testing.T) {
	fixture := newW7CatalogFixture(t)
	ctx := context.Background()
	svc := service.NewAdminProductService(fixture.store)
	category := "0192f0a2-0000-0000-0000-000000000001"

	created, err := svc.CreateProduct(ctx, category, "Produk Baru", "produk-baru", "deskripsi", "")
	if err != nil {
		t.Fatalf("create with free slug: %v", err)
	}
	if created.ID == "" || created.Slug != "produk-baru" {
		t.Fatalf("created product = %#v", created)
	}
	if _, err := svc.CreateProduct(ctx, category, "Produk Lain", "produk-baru", "", ""); !errors.Is(err, service.ErrConflict) {
		t.Fatalf("duplicate slug error = %v, want ErrConflict", err)
	}
	updated, err := svc.UpdateProduct(ctx, created.ID, "", "Produk Baru", "slug-baru", "", "", nil)
	if err != nil {
		t.Fatalf("update with free slug: %v", err)
	}
	// Re-submitting the product's own slug is a normal edit (name/status), not
	// a duplicate conflict and never a nil-wrapped error.
	if _, err := svc.UpdateProduct(ctx, updated.ID, "", "Nama Baru", updated.Slug, "", "", nil); err != nil {
		t.Fatalf("update with unchanged slug: %v", err)
	}
	if _, err := svc.UpdateProduct(ctx, uuid.NewString(), "", "", "slug-lain", "", "", nil); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("update missing product error = %v, want ErrNotFound", err)
	}
	if err := svc.DeleteProduct(ctx, uuid.NewString()); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("delete missing product error = %v, want ErrNotFound", err)
	}
	if _, err := svc.GetProduct(ctx, uuid.NewString()); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("get missing product error = %v, want ErrNotFound", err)
	}
	if _, err := svc.CreateProduct(ctx, "Streaming", "Nama", "slug-streaming", "", ""); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("malformed category error = %v, want ErrInvalidInput", err)
	}
	if _, err := svc.CreateProduct(ctx, uuid.NewString(), "Nama", "slug-unknown-category", "", ""); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("unknown category error = %v, want ErrInvalidInput", err)
	}

	categorySvc := service.NewAdminCategoryService(fixture.store)
	createdCategory, err := categorySvc.CreateCategory(ctx, "Kategori Baru", "kategori-baru", "", "")
	if err != nil {
		t.Fatalf("create category with free slug: %v", err)
	}
	if _, err := categorySvc.CreateCategory(ctx, "Kategori Lain", "kategori-baru", "", ""); !errors.Is(err, service.ErrConflict) {
		t.Fatalf("duplicate category slug error = %v, want ErrConflict", err)
	}
	updatedCategory, err := categorySvc.UpdateCategory(ctx, createdCategory.ID, "", "kategori-baru-lagi", "", "", nil)
	if err != nil {
		t.Fatalf("update category with free slug: %v", err)
	}
	if _, err := categorySvc.UpdateCategory(ctx, updatedCategory.ID, "Nama Kategori Baru", updatedCategory.Slug, "", "", nil); err != nil {
		t.Fatalf("update category with unchanged slug: %v", err)
	}
	if err := categorySvc.DeleteCategory(ctx, uuid.NewString()); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("delete missing category error = %v, want ErrNotFound", err)
	}
}

func TestW7AdminProductActiveFilterAndDurations(t *testing.T) {
	fixture := newW7CatalogFixture(t)
	ctx := context.Background()
	svc := service.NewAdminProductService(fixture.store)

	active := true
	products, total, err := svc.ListProducts(ctx, service.AdminProductFilter{Active: &active}, 1, 20)
	if err != nil {
		t.Fatalf("active=true: %v", err)
	}
	if total != 5 {
		t.Fatalf("active=true total = %d, want 5", total)
	}
	for _, product := range products {
		if !product.Active {
			t.Fatalf("active=true returned inactive product %s", product.Slug)
		}
	}

	inactive := false
	products, total, err = svc.ListProducts(ctx, service.AdminProductFilter{Active: &inactive}, 1, 20)
	if err != nil {
		t.Fatalf("active=false: %v", err)
	}
	if total != 1 || products[0].ID != fixture.products["canva"] {
		t.Fatalf("active=false = %d %v, want canva only", total, productIDsFromProducts(products))
	}

	products, total, err = svc.ListProducts(ctx, service.AdminProductFilter{}, 1, 20)
	if err != nil {
		t.Fatalf("active omitted: %v", err)
	}
	if total != 6 {
		t.Fatalf("active omitted total = %d, want 6", total)
	}

	var netflix *domain.Product
	for index := range products {
		if products[index].ID == fixture.products["netflix"] {
			netflix = &products[index]
		}
	}
	if netflix == nil || len(netflix.Prices) != 3 || netflix.Prices[0].Duration == nil {
		t.Fatalf("admin product list did not preload price durations: %#v", netflix)
	}
	// Admin prices are sorted deterministically by duration days, then amount,
	// so the result does not depend on physical row order.
	if netflix.Prices[0].Amount != 50000 || netflix.Prices[1].Amount != 186000 || netflix.Prices[2].Amount != 250000 {
		t.Fatalf("admin price order = %d/%d/%d, want 50000/186000/250000", netflix.Prices[0].Amount, netflix.Prices[1].Amount, netflix.Prices[2].Amount)
	}
	if netflix.Prices[0].Duration.Days != 30 {
		t.Fatalf("first admin duration = %d, want 30", netflix.Prices[0].Duration.Days)
	}
	if len(netflix.Labels) != 1 || netflix.Labels[0] != "Best Seller" || len(netflix.DeviceOptions) != 2 {
		t.Fatalf("labels/device options did not round-trip: %#v", netflix)
	}

	accounts, total, err := fixture.store.ListAccounts(ctx, 1, 20, "")
	if err != nil {
		t.Fatalf("list inventory accounts: %v", err)
	}
	if total != 4 || len(accounts) != 4 || accounts[0].Product == nil {
		t.Fatalf("inventory accounts = %d/%d first=%#v", total, len(accounts), accounts)
	}
}

func productIDsFromProducts(products []domain.Product) []string {
	ids := make([]string, len(products))
	for index, product := range products {
		ids[index] = product.ID
	}
	return ids
}

type w7DashboardFixture struct {
	db         *gorm.DB
	store      *Store
	customerID string
	adminID    string
	paidItem   string
}

func newW7DashboardFixture(t *testing.T) *w7DashboardFixture {
	t.Helper()
	db := openW7DB(t)
	now := time.Now().UTC()

	customerID := "0192f0c0-0000-0000-0000-000000000001"
	adminID := "0192f0c0-0000-0000-0000-000000000003"
	users := []domain.User{
		{ID: customerID, Email: "fixture.customer@example.test", Name: "Fixture Customer", PasswordHash: "x", Role: domain.RoleCustomer, Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: adminID, Email: "fixture.admin@example.test", Name: "Fixture Admin", PasswordHash: "x", Role: domain.RoleAdmin, Active: true, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}

	categoryID := "0192f0a2-0000-0000-0000-000000000001"
	productID := "0192f0a3-0000-0000-0000-000000000001"
	durationID := "0192f0a1-0000-0000-0000-000000000001"
	priceID := "0192f0a4-0000-0000-0000-000000000001"
	if err := db.Create(&domain.Category{ID: categoryID, Name: "Streaming Video", Slug: "video-streaming", Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.Product{ID: productID, CategoryID: categoryID, Name: "Netflix Premium", Slug: "netflix-premium", Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.ProductDuration{ID: durationID, Name: "1 Bulan", Days: 30, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.ProductPrice{ID: priceID, ProductID: productID, DurationID: durationID, Amount: 167400, Currency: "IDR", Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	paidItem := uuid.NewString()
	pendingItem := uuid.NewString()
	orders := []domain.Order{
		{ID: uuid.NewString(), Number: "ORD-W7-PAID", UserID: customerID, Status: domain.OrderPaid, Subtotal: 167400, Total: 167400, Currency: "IDR", PaymentDueAt: now.Add(15 * time.Minute), IdempotencyKey: "w7-paid", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.NewString(), Number: "ORD-W7-PENDING", UserID: customerID, Status: domain.OrderPending, Subtotal: 50000, Total: 50000, Currency: "IDR", PaymentDueAt: now.Add(15 * time.Minute), IdempotencyKey: "w7-pending", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.NewString(), Number: "ORD-W7-CANCELLED", UserID: customerID, Status: domain.OrderCancelled, Subtotal: 40000, Total: 40000, Currency: "IDR", PaymentDueAt: now.Add(15 * time.Minute), IdempotencyKey: "w7-cancelled", CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&orders).Error; err != nil {
		t.Fatalf("seed orders: %v", err)
	}
	items := []domain.OrderItem{
		{ID: paidItem, OrderID: orders[0].ID, ProductPriceID: priceID, ProductName: "Netflix Premium", DurationName: "1 Bulan", UnitPrice: 167400, Quantity: 1, DeviceCount: 1, LineTotal: 167400},
		{ID: pendingItem, OrderID: orders[1].ID, ProductPriceID: priceID, ProductName: "Netflix Premium", DurationName: "1 Bulan", UnitPrice: 50000, Quantity: 1, DeviceCount: 1, LineTotal: 50000},
	}
	if err := db.Create(&items).Error; err != nil {
		t.Fatalf("seed order items: %v", err)
	}

	accountID := uuid.NewString()
	if err := db.Create(&domain.InventoryAccount{ID: accountID, ProductID: productID, EncryptedCredentials: []byte("fixture"), Status: "active", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	slots := []domain.InventorySlot{
		{ID: uuid.NewString(), InventoryAccountID: accountID, ProductPriceID: priceID, Status: "available", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.NewString(), InventoryAccountID: accountID, ProductPriceID: priceID, Status: "available", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.NewString(), InventoryAccountID: accountID, ProductPriceID: priceID, Status: "available", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.NewString(), InventoryAccountID: accountID, ProductPriceID: priceID, Status: "reserved", OrderItemID: &paidItem, CreatedAt: now, UpdatedAt: now},
		{ID: uuid.NewString(), InventoryAccountID: accountID, ProductPriceID: priceID, Status: "reserved", OrderItemID: &pendingItem, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&slots).Error; err != nil {
		t.Fatalf("seed slots: %v", err)
	}

	return &w7DashboardFixture{db: db, store: NewStore(db), customerID: customerID, adminID: adminID, paidItem: paidItem}
}

func TestW7AdminDashboardDTOAndAggregates(t *testing.T) {
	fixture := newW7DashboardFixture(t)
	ctx := context.Background()

	stats, err := fixture.store.GetStats(ctx)
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	expected := map[string]int64{
		"total_users":        2,
		"total_orders":       3,
		"total_revenue":      167400,
		"pending_orders":     1,
		"new_users_today":    2,
		"orders_today":       3,
		"revenue_today":      167400,
		"active_activations": 1,
		"available_slots":    3,
	}
	if stats.ActiveUsers != 2 {
		t.Fatalf("active_users = %d, want 2", stats.ActiveUsers)
	}

	encoded, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	for key, want := range expected {
		raw, ok := payload[key]
		if !ok {
			t.Fatalf("dashboard JSON is missing %q: %s", key, encoded)
		}
		var got int64
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("%s is not a number: %s", key, raw)
		}
		if got != want {
			t.Fatalf("%s = %d, want %d", key, got, want)
		}
	}
	for _, key := range []string{"active_activations", "available_slots"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("dashboard JSON is missing %q", key)
		}
	}

	// A failed query must be reported, not silently converted into zeroes.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := fixture.store.GetStats(cancelled); err == nil {
		t.Fatal("GetStats with a cancelled context returned nil error (false zeros)")
	}
}

func TestW7AdminAuditDTOKeysAndFilters(t *testing.T) {
	fixture := newW7DashboardFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()

	loginAuditID := "0192f0d0-0000-0000-0000-000000000001"
	// resource_id, ip_address and metadata are nullable in the schema; the DTO
	// must still return a stable shape instead of failing the whole list.
	if err := fixture.db.Exec(
		`INSERT INTO audit_logs (id, actor_user_id, action, resource_type, resource_id, ip_address, metadata, created_at)
		 VALUES (?, ?, 'auth.login.succeeded', 'user', NULL, NULL, NULL, ?)`,
		loginAuditID, fixture.customerID, now,
	).Error; err != nil {
		t.Fatalf("insert login audit: %v", err)
	}
	systemAuditID := "0192f0d0-0000-0000-0000-000000000002"
	if err := fixture.db.Exec(
		`INSERT INTO audit_logs (id, actor_user_id, action, resource_type, resource_id, ip_address, metadata, created_at)
		 VALUES (?, NULL, 'system.job', 'system', '', '', NULL, ?)`,
		systemAuditID, now,
	).Error; err != nil {
		t.Fatalf("insert system audit: %v", err)
	}

	logs, total, err := fixture.store.ListAuditLogs(ctx, service.AuditLogFilter{Action: "auth.login.succeeded"}, 1, 50)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if total != 1 || len(logs) != 1 {
		t.Fatalf("filtered audit = %d/%d, want 1", total, len(logs))
	}
	encoded, err := json.Marshal(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "actor_user_id", "actor_user", "action", "resource_type", "resource_id", "ip_address", "metadata", "created_at"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("audit JSON is missing %q: %s", key, encoded)
		}
	}
	var actor map[string]any
	if err := json.Unmarshal(payload["actor_user"], &actor); err != nil {
		t.Fatal(err)
	}
	if actor["email"] != "fixture.customer@example.test" {
		t.Fatalf("audit actor = %#v", actor)
	}
	var ipAddress string
	_ = json.Unmarshal(payload["ip_address"], &ipAddress)
	if string(payload["resource_id"]) != "null" {
		t.Fatalf("resource_id = %s, want explicit null", payload["resource_id"])
	}
	if ipAddress != "" {
		t.Fatalf("ip_address = %q, want empty string", ipAddress)
	}
	if string(payload["metadata"]) != "null" {
		t.Fatalf("metadata = %s, want null", payload["metadata"])
	}

	logs, total, err = fixture.store.ListAuditLogs(ctx, service.AuditLogFilter{Action: "auth.login.failed"}, 1, 50)
	if err != nil {
		t.Fatalf("filter empty action: %v", err)
	}
	if total != 0 || len(logs) != 0 {
		t.Fatalf("empty filter = %d/%d, want 0", total, len(logs))
	}

	logs, _, err = fixture.store.ListAuditLogs(ctx, service.AuditLogFilter{Action: "system.job"}, 1, 50)
	if err != nil {
		t.Fatalf("system audit: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("system audit rows = %d, want 1", len(logs))
	}
	encoded, _ = json.Marshal(logs[0])
	payload = map[string]json.RawMessage{}
	_ = json.Unmarshal(encoded, &payload)
	if string(payload["actor_user_id"]) != "null" || string(payload["actor_user"]) != "null" {
		t.Fatalf("nil actor keys = %s/%s, want explicit nulls", payload["actor_user_id"], payload["actor_user"])
	}
}

func TestW7AdminMetadataDTOKeys(t *testing.T) {
	fixture := newW7DashboardFixture(t)
	ctx := context.Background()

	// The customer was seeded without email verification. The key must still be
	// present as null so the admin table can distinguish unverified from
	// "field missing".
	user, err := fixture.store.UserByID(ctx, fixture.customerID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(user)
	if err != nil {
		t.Fatal(err)
	}
	var userPayload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &userPayload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"active", "email_verified_at"} {
		if _, ok := userPayload[key]; !ok {
			t.Fatalf("admin user JSON is missing %q: %s", key, encoded)
		}
	}
	if string(userPayload["email_verified_at"]) != "null" {
		t.Fatalf("unverified email_verified_at = %s, want null", userPayload["email_verified_at"])
	}

	now := time.Now().UTC()
	voucher := &domain.Voucher{
		ID: uuid.NewString(), Code: "W7VOUCHER", Type: domain.VoucherPercentage, Value: 10,
		MinOrder: 50000, MaxDiscount: 50000, UsageLimit: 100, PerUserLimit: 1,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(24 * time.Hour), Active: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.db.Create(voucher).Error; err != nil {
		t.Fatalf("seed voucher: %v", err)
	}
	vouchers, total, err := fixture.store.ListVouchers(ctx, service.VoucherFilter{}, 1, 20)
	if err != nil {
		t.Fatalf("list vouchers: %v", err)
	}
	if total != 1 || len(vouchers) != 1 {
		t.Fatalf("vouchers = %d/%d, want 1", total, len(vouchers))
	}
	encoded, err = json.Marshal(vouchers[0])
	if err != nil {
		t.Fatal(err)
	}
	var voucherPayload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &voucherPayload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"type", "usage_limit", "per_user_limit", "starts_at", "ends_at", "active"} {
		if _, ok := voucherPayload[key]; !ok {
			t.Fatalf("voucher JSON is missing %q: %s", key, encoded)
		}
	}
}
