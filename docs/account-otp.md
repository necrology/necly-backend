# Ambil OTP email akun yang dibeli (NEC-93)

Platform langganan (misalnya Netflix atau layanan lain) kadang meminta kode OTP
yang dikirim ke email akun. Pembeli tidak boleh diberi kata sandi mailbox
tersebut. Alur ini memberi kode akses sementara yang hanya bisa dipakai untuk
membaca OTP terbaru milik akun yang memang ia beli.

## Alur pembeli

1. Pembeli membuka detail pesanan setelah admin menyetujui dan mengirim akun.
2. Pada item yang sudah terkirim, halaman menampilkan **email akun** dan tombol
   **Buat kode akses OTP**.
3. Server membuat kode 10 karakter yang berlaku 15 menit, menyimpan hanya
   hash-nya, lalu mengirim kode itu ke email pembeli yang terverifikasi.
4. Pembeli membuka `/otp`, memasukkan email akun dan kode akses.
5. Server mencocokkan hash kode dan email, membaca email terbaru yang
   dialamatkan ke email akun tersebut, lalu menampilkan kode OTP.

Kode akses tidak pernah disimpan dalam bentuk plaintext. Email akun hanya
ditampilkan kepada pemilik pesanan; kata sandi akun tetap tidak pernah
dikembalikan API.

## Kontrak API

### `GET /api/v1/orders/:id/account-access` (login)

Mengembalikan akun yang sudah dikirim untuk pesanan milik pengguna.

```json
{
  "items": [
    {
      "order_item_id": "uuid",
      "product_name": "Netflix Premium",
      "duration_name": "1 Bulan",
      "account_label": "Akun utama",
      "account_email": "family@example.test"
    }
  ],
  "otp_inbox_ready": true
}
```

- Pesanan milik pengguna lain dijawab `404` (tidak membedakan "tidak ada" dan
  "bukan milik Anda").
- Pesanan yang belum `processing` atau lebih tinggi dijawab
  `409 order_not_approved`.
- Item yang belum berstatus `sent` tidak ditampilkan.

### `POST /api/v1/orders/:id/account-access/:itemId/code` (login)

Membuat kode akses untuk satu item yang sudah terkirim.

```json
{
  "code": "ABCD234567",
  "expires_at": "2026-10-21T17:30:00Z",
  "emails": ["family@example.test"],
  "email_sent": true
}
```

- `503 otp_unavailable` bila Redis atau mailbox OTP belum dikonfigurasi.
- `409 order_not_approved` bila akun belum dikirim.
- `404` bila item bukan bagian dari pesanan pengguna.

### `POST /api/v1/account-otp/lookup` (publik, rate-limited)

```json
{ "email": "family@example.test", "code": "ABCD234567" }
```

```json
{ "otp": "482913", "received_at": "2026-10-21T17:05:00Z" }
```

Kesalahan:

- `404 otp_access_invalid` — email dan kode tidak cocok, kode kedaluwarsa, atau
  kode tidak ada. Pesannya sengaja sama agar akun tidak bisa ditebak.
- `404 otp_not_found` — belum ada email OTP baru untuk akun tersebut.
- `503 otp_unavailable` — mailbox OTP belum dikonfigurasi.
- `503 otp_lookup_failed` — mailbox tidak dapat dibaca; tidak ada detail
  internal yang dibocorkan.

## Konfigurasi operator

Isi variabel berikut pada deployment backend:

```
OTP_IMAP_ADDR=imap.gmail.com:993
OTP_IMAP_USER=otp@necly.example
OTP_IMAP_PASSWORD=<app password>
OTP_IMAP_MAILBOX=INBOX
OTP_IMAP_TLS=true
OTP_IMAP_LOOKBACK=15m
OTP_IMAP_TIMEOUT=20s
```

Semua email OTP untuk akun yang dijual harus masuk ke mailbox ini. Bila memakai
forwarding, pertahankan alamat asli pada header `Delivered-To`,
`X-Original-To`, atau `To`. Tanpa header tersebut pesan sengaja diabaikan agar
satu pembeli tidak bisa membaca OTP pembeli lain. Redis wajib aktif karena kode
akses disimpan di sana dengan TTL.

## Batas keamanan

- Kode akses: 10 karakter acak, TTL 15 menit, hanya hash SHA-256 yang disimpan.
- Endpoint lookup publik dibatasi 10 percobaan/menit per IP; endpoint pembuatan
  kode hanya untuk pemilik pesanan.
- Email, kode akses, dan OTP tidak pernah ditulis ke log, audit, atau pesan
  error.
- Mailbox hanya dibaca (IMAP `EXAMINE`) dan koneksi wajib TLS.
- OTP yang ditampilkan adalah kode terbaru yang dialamatkan ke email akun dan
  masuk dalam lookback window; tidak ada pembacaan seluruh isi mailbox ke klien.
