// Perintah sekali jalan untuk mengisi kapasitas jual.
//
// Katalog menghitung ketersediaan dari baris inventory_slots berstatus
// available untuk sebuah product_price, dan checkout mengunci baris yang sama.
// Selama belum ada baris itu, tidak satu pun paket bisa dibeli — dan karena
// inventory_slots.inventory_account_id wajib diisi, tiap produk perlu akun
// induknya lebih dulu.
//
// Sifatnya idempoten: akun yang sudah ada dipakai ulang, dan jumlah slot per
// paket dilengkapi sampai mencapai target, bukan ditambah tanpa batas. Aman
// dijalankan berulang.
//
// Jalankan di dalam container api supaya konfigurasi database dan kunci
// enkripsi kredensial diambil dari lingkungan yang sama dengan server:
//
//	docker compose exec api ./necly-seed-inventory -per-price 10
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/necly/services/internal/config"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/platform/database"
	"github.com/necly/services/internal/repository/gormrepo"
	"github.com/necly/services/internal/service"
)

// Kredensial akun induk tidak bisa dikarang, jadi diisi penanda yang jelas
// supaya terlihat di dashboard dan diganti sebelum dipakai berjualan.
const placeholderCredentials = "BELUM DIISI - ganti dari Dashboard > Inventaris"

func main() {
	perPrice := flag.Int("per-price", 10, "jumlah slot yang disiapkan untuk tiap paket")
	flag.Parse()

	if *perPrice < 1 || *perPrice > 100 {
		log.Fatalf("per-price harus antara 1 dan 100, bukan %d", *perPrice)
	}

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

	cipher, err := service.NewCredentialCipher(cfg.CredentialKey)
	if err != nil {
		log.Fatalf("credential cipher: %v", err)
	}

	store := gormrepo.NewStore(db)
	inventory := service.NewAdminInventoryService(store, cipher)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Ketersediaan tiap paket sudah ikut pada hasil ini (available_slots), jadi
	// tidak perlu query terpisah untuk menghitungnya.
	active := true
	products, total, err := store.ListProductsWithStock(ctx, service.ProductFilter{Active: &active}, 1, 200)
	if err != nil {
		log.Fatalf("list products: %v", err)
	}

	fmt.Printf("%d produk aktif\n", total)
	accountsCreated, slotsCreated, slotsExisting := 0, 0, 0

	for _, product := range products {
		// Akun induk per produk: baris slot wajib menunjuk salah satunya.
		if _, err := store.GetAccountByProductID(ctx, product.ID); err != nil {
			if _, err := inventory.CreateInventoryAccount(ctx, service.CreateInventoryAccountInput{
				ProductID:   product.ID,
				Credentials: placeholderCredentials,
			}); err != nil {
				log.Fatalf("buat akun untuk %s: %v", product.Name, err)
			}
			accountsCreated++
			fmt.Printf("  akun induk dibuat untuk %s\n", product.Name)
		}

		for _, price := range product.Prices {
			slotsExisting += price.AvailableSlots

			missing := *perPrice - price.AvailableSlots
			if missing <= 0 {
				continue
			}
			created, err := inventory.CreateSlots(ctx, service.CreateSlotsInput{
				ProductPriceID: price.ID,
				Quantity:       missing,
			})
			if err != nil {
				log.Fatalf("buat slot %s: %v", product.Name, err)
			}
			slotsCreated += created
			fmt.Printf("  +%d slot untuk %s (%s)\n", created, product.Name, durationName(price))
		}
	}

	fmt.Printf("\nselesai: %d akun dibuat, %d slot ditambahkan, %d slot sudah ada sebelumnya\n", accountsCreated, slotsCreated, slotsExisting)
}

func durationName(price domain.ProductPriceWithStock) string {
	if price.Duration != nil && price.Duration.Name != "" {
		return price.Duration.Name
	}
	return price.DurationID
}
