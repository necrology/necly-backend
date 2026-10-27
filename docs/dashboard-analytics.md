# Analitik dashboard dan master kategori (NEC-131)

Dokumen ini adalah kontrak backend untuk chart pada halaman Ringkasan dan
baseline master kategori. Endpoint memakai prefix yang sama dengan endpoint
admin lain: `/api/v1/admin`.

## `GET /api/v1/admin/dashboard/analytics`

Hanya dapat diakses admin yang sudah terautentikasi. Endpoint lama
`GET /api/v1/admin/dashboard` tetap tersedia tanpa perubahan bentuk response.

### Parameter

| Parameter | Nilai | Default | Perilaku |
|---|---|---|---|
| `days` | `7`, `30`, `90` | `7` | Selain nilai itu, termasuk `0` atau teks, ditolak `400` dengan `code: "invalid_input"`. |

Rentang bersifat inklusif sampai hari ini dalam UTC. `days=7` berarti hari ini
dan enam hari sebelumnya. Query database memakai rentang setengah terbuka
`[awal_hari_pertama, awal_hari_berikutnya)` agar satu order tidak terhitung dua
kali di batas hari.

### Response `200`

```json
{
  "range_days": 7,
  "from": "2026-10-21",
  "to": "2026-10-27",
  "revenue_series": [{"date": "2026-10-21", "amount": 0}],
  "orders_series": [{"date": "2026-10-21", "orders": 3}],
  "status_breakdown": [{"status": "paid", "count": 2}],
  "top_products": [{"product_id": "...", "product_name": "...", "sales_count": 4, "revenue": 123000}],
  "category_breakdown": [{"category_id": "...", "category_name": "...", "orders": 3, "revenue": 100000}]
}
```

`revenue_series` dan `orders_series` selalu berisi satu titik untuk setiap
tanggal dalam rentang. Tanggal tanpa order diisi `0` (zero-filled). Jika tidak
ada data, `status_breakdown`, `top_products`, dan `category_breakdown` adalah
array kosong, bukan `null`.

### Definisi metrik

- `revenue_series`: `SUM(orders.total)` per hari UTC untuk status `paid`,
  `fulfilling`, dan `completed`, berdasarkan `orders.created_at`.
- `orders_series`: `COUNT(*)` semua order per hari UTC, tanpa filter status.
- `status_breakdown`: `COUNT(*)` per `orders.status` dalam rentang. Status baru
  tetap diteruskan; label menjadi tanggung jawab frontend.
- `top_products`: dari `order_items` JOIN `orders`, hanya status revenue, urut
  `revenue DESC`, maksimal 5. `sales_count = SUM(quantity)`,
  `revenue = SUM(line_total)`. `product_id` mengikuti endpoint lama, yaitu
  `order_items.product_price_id`.
- `category_breakdown`: dari `order_items` → `orders` → `product_prices` →
  `products` → `categories`. Hanya kategori yang memiliki penjualan.
  `orders = COUNT(DISTINCT orders.id)` dan `revenue = SUM(order_items.line_total)`.

Jika salah satu query gagal, endpoint mengembalikan `500` dengan pesan generik
`Gagal mengambil analitik dashboard`. Tidak ada data contoh atau fallback palsu.

## Migrasi `021_master_categories.sql`

Migrasi menambahkan empat kategori awal bila slug-nya belum ada:
`video-streaming`, `music-streaming`, `productivity-suites`, dan
`cloud-storage`.

- Tidak pernah mengubah nama, deskripsi, atau status kategori yang sudah ada.
- Tidak menghapus baris apa pun.
- Pemeriksaan slug tidak memfilter `deleted_at`, sehingga kategori yang sudah
  soft-deleted tidak dibuat ulang dan unique slug tidak dilanggar.
- `ON CONFLICT DO NOTHING` menjadi pengaman kedua.
- ID yang dipakai sama dengan seed pengembangan `002_seed_data.sql`, sehingga
  instalasi yang sudah menjalankan seed tidak mengalami konflik slug dan produk
  seed tetap menunjuk kategori yang benar.
- Aman dijalankan berulang; migrasi juga dapat dieksekusi ulang tanpa error.
