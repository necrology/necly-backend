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
		// Satu akun maksimal melayani lima user. Seed lama memakai satu akun
		// dengan limit sebesar target; sejak aturan Fase 2 itu tidak valid, jadi
		// seed membagi kapasitas ke beberapa akun (masing-masing <= 5 seat).
		accounts, _, err := store.ListAccounts(ctx, 1, 100, product.ID, "")
		if err != nil {
			log.Fatalf("baca akun %s: %v", product.Name, err)
		}

		for _, price := range product.Prices {
			slotsExisting += price.AvailableSlots

			missing := *perPrice - price.AvailableSlots
			if missing <= 0 {
				continue
			}
			createdForPrice := 0
			for missing > 0 {
				index, capacity := seedCapacity(accounts, time.Now().UTC())
				if capacity == 0 {
					limit := missing
					if limit > service.MaxSharingLimit {
						limit = service.MaxSharingLimit
					}
					account, err := inventory.CreateInventoryAccount(ctx, service.CreateInventoryAccountInput{
						ProductID:    product.ID,
						Credentials:  placeholderCredentials,
						SharingLimit: limit,
					})
					if err != nil {
						log.Fatalf("buat akun untuk %s: %v", product.Name, err)
					}
					accounts = append(accounts, *account)
					index = len(accounts) - 1
					capacity = limit
					accountsCreated++
					fmt.Printf("  akun dibuat untuk %s (limit %d)\n", product.Name, limit)
				}
				quantity := missing
				if quantity > capacity {
					quantity = capacity
				}
				result, err := inventory.CreateSlots(ctx, service.CreateSlotsInput{
					ProductPriceID:     price.ID,
					InventoryAccountID: accounts[index].ID,
					Quantity:           quantity,
				})
				if err != nil {
					log.Fatalf("buat slot %s: %v", product.Name, err)
				}
				accounts[index].TotalSlots += result.Created
				accounts[index].AvailableSlots += result.Created
				missing -= result.Created
				createdForPrice += result.Created
			}
			slotsCreated += createdForPrice
			fmt.Printf("  +%d slot untuk %s (%s)\n", createdForPrice, product.Name, durationName(price))
		}
	}

	fmt.Printf("\nselesai: %d akun dibuat, %d slot ditambahkan, %d slot sudah ada sebelumnya\n", accountsCreated, slotsCreated, slotsExisting)
}

// seedCapacity finds an active account that may still accept seats. Accounts
// above the five-user rule are skipped exactly like the admin API does, so the
// seed never deepens a legacy violation.
func seedCapacity(accounts []domain.InventoryAccount, now time.Time) (int, int) {
	for i := range accounts {
		account := accounts[i]
		if account.Status != service.InventoryStatusActive {
			continue
		}
		if account.ExpiresAt != nil && !account.ExpiresAt.After(now) {
			continue
		}
		if account.SharingLimit > service.MaxSharingLimit {
			continue
		}
		if remaining := account.SharingLimit - account.TotalSlots; remaining > 0 {
			return i, remaining
		}
	}
	return -1, 0
}

func durationName(price domain.ProductPriceWithStock) string {
	if price.Duration != nil && price.Duration.Name != "" {
		return price.Duration.Name
	}
	return price.DurationID
}
