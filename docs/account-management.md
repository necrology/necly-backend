# Manajemen akun inventaris (NEC-89 Fase 2)

Dokumen ini adalah kontrak backend untuk menu **Manajemen akun**. Implementasi
mencakup CRUD akun, agregat pemakaian, batas lima pengguna per akun, dan OTP
TOTP. Endpoint tetap memakai prefix `/api/v1/admin/inventory` yang sudah dipakai
dashboard Fase 1; nama route tidak diubah agar klien lama tidak rusak.

## Aturan bisnis

- **Maksimal 5 pengguna per akun.** `sharing_limit` untuk create/update harus
  1–5 (create tanpa nilai memakai default 1). Akun lama dengan limit di atas 5
  tidak dipotong otomatis: seluruh slot yang sudah terjual tetap utuh, tetapi
  response menandai `needs_review=true` dan permintaan slot baru ditolak dengan
  `409 needs_review`.
- Menurunkan limit di bawah jumlah slot yang sudah terprovisi tetap ditolak
  dengan `409 sharing_limit_exceeded`; tidak ada slot yang dihapus diam-diam.
- Hapus akun ditolak dengan `409 inventory_account_in_use` bila masih ada slot
  yang terpakai, dipesan, atau tertaut pesanan.
- **OTP bersifat opsional.** Secret TOTP disimpan terenkripsi dengan
  `CredentialCipher`, tidak pernah diserialisasi, dan hanya dipakai untuk
  menghasilkan kode.

## Bentuk response akun

`GET /api/v1/admin/inventory`, `GET /api/v1/admin/inventory/:id`, create, dan
update mengembalikan field turunan berikut:

| Field | Arti |
|---|---|
| `assigned_users` | jumlah user unik yang memiliki slot pada akun |
| `total_slots` / `available_slots` / `used_slots` | kapasitas dari baris `inventory_slots` |
| `remaining_slots` | sisa kapasitas terhadap `sharing_limit` (tidak pernah negatif) |
| `days_remaining` | sisa hari sampai `expires_at`; `null` berarti tanpa batas, `0` berarti sudah kedaluwarsa |
| `otp_configured` | apakah secret TOTP tersimpan |
| `needs_review` | akun lama di atas limit lima pengguna |
| `sellable` | status aktif dan belum kedaluwarsa |

`encrypted_credentials` dan `otp_secret_encrypted` tidak pernah muncul di JSON.

## Create dan update

```json
{
  "product_id": "uuid",
  "credentials": "username:password",
  "label": "Akun utama",
  "sharing_limit": 5,
  "expires_at": "2026-12-31T23:59:59Z",
  "otp_secret": "JBSWY3DPEHPK3PXP"
}
```

- `otp_secret` hanya untuk ditulis (write-only). Secret base32 divalidasi;
  spasi, huruf kecil, dan padding `=` diterima lalu dinormalisasi.
- Pada `PATCH`, field `otp_secret` yang **tidak dikirim** mempertahankan secret
  lama. `otp_secret: ""` menghapus secret.
- `sharing_limit` di atas 5 ditolak `400 invalid_input`.

## OTP

`POST /api/v1/admin/inventory/:id/otp`

Response `200`:

```json
{ "code": "123456", "expires_in": 17, "period": 30 }
```

- Kode enam digit, RFC 6238, HMAC-SHA1, langkah waktu 30 detik.
- Akun tanpa secret menjawab `409 otp_not_configured`.
- Endpoint hanya untuk admin dan dibatasi 10 permintaan per menit untuk pasangan
  akun + agen. Melewati batas menjawab `429 rate_limited`.
- Kode, secret, dan ciphertext tidak ditulis ke log, audit, atau pesan error.

## Detail slot

`GET /api/v1/admin/inventory/:id/slots` menambahkan:

| Field | Arti |
|---|---|
| `activated_at` | waktu slot mulai aktif untuk pembeli (diisi alur pengiriman) |
| `access_expires_at` | akhir masa akses pembeli untuk slot tersebut |
| `days_remaining` | sisa hari dari `access_expires_at`; `null` bila belum diisi |
| `order_number` | nomor pesanan pemilik slot |
| `payment_status` | status pembayaran terakhir pesanan, bila ada |
| `reserved_email` / `reserved_name` | identitas pembeli |

Response slot tidak pernah memuat kredensial akun.
