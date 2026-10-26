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

## Sumber mailbox: hybrid per akun + fallback pusat

Sejak NEC-121, lookup OTP mencoba dua sumber secara berurutan:

1. **Mailbox akun sendiri (`direct`).** Bila manajemen akun menyimpan
   `account_email` dan `mailbox_app_password`, service membuka mailbox akun itu
   lewat IMAP TLS read-only dan hanya menerima pesan yang dialamatkan ke email
   tersebut. Koneksi langsung dibatasi 18 detik.
2. **Mailbox pusat (`central`).** Bila akun tidak ditemukan, app password kosong,
   dekripsi gagal, atau pembacaan langsung gagal, service memakai mailbox
   `OTP_IMAP_*` yang sudah ada.

Sumber yang dipakai tidak dibocorkan ke response publik. `GET
/api/v1/admin/inventory` menampilkan `otp_inbox_source` (`direct`, `central`,
atau `none`) dan `otp_inbox_configured` agar operator tahu kesiapan tiap akun.
Tidak ada background poller; mailbox dibaca saat pembeli meminta OTP.

Kegagalan tetap fail-closed:

- Tidak ada app password dan mailbox pusat tidak dikonfigurasi →
  `503 otp_unavailable`.
- Mailbox terbaca tetapi belum ada pesan OTP baru → `404 otp_not_found`.
- Kode akses salah/kedaluwarsa tetap `otp_access_invalid`; pencarian mailbox
  tidak pernah dijalankan sebelum kode akses diverifikasi.

## Membuat Gmail app password

Untuk akun Gmail yang dipakai membaca OTP:

1. Aktifkan verifikasi dua langkah (2FA) pada akun Google tersebut. Google
   tidak menampilkan menu app password tanpa 2FA.
2. Buka **Google Account → Security → App passwords**, buat password khusus
   untuk aplikasi Mail/IMAP.
3. Simpan password 16 karakter itu di field `mailbox_app_password` pada
   manajemen akun. Password login akun tetap disimpan terpisah di
   `account_password`.
4. Pastikan IMAP aktif untuk akun tersebut. Field `mailbox_imap_host` hanya
   menerima `imap.gmail.com` atau `imap.googlemail.com` (opsional `:993`);
   host/port lain ditolak `400 invalid_input` dan reader menolak dial ke tujuan
   di luar allowlist tersebut.

Jika Google Workspace organisasi melarang app password, akun tersebut tidak
bisa memakai sumber `direct`. Jangan memaksa password utama akun ke kolom app
password: gunakan fallback mailbox pusat atau minta administrator Workspace
mengizinkan app password. App password adalah kredensial sensitif dan tidak
pernah dikembalikan API, ditulis ke log, atau masuk audit.

## Konfigurasi operator

Variabel `OTP_IMAP_*` tetap dipertahankan sebagai mailbox pusat/fallback:

```
OTP_IMAP_ADDR=imap.gmail.com:993
OTP_IMAP_USER=otp@necly.example
OTP_IMAP_PASSWORD=<app password>
OTP_IMAP_MAILBOX=INBOX
OTP_IMAP_TLS=true
OTP_IMAP_LOOKBACK=15m
OTP_IMAP_TIMEOUT=20s
```

Bila memakai forwarding ke mailbox pusat, pertahankan alamat asli pada header
`Delivered-To`, `X-Original-To`, atau `To`. Tanpa header tersebut pesan sengaja
diabaikan agar satu pembeli tidak bisa membaca OTP pembeli lain. Redis wajib
aktif karena kode akses disimpan di sana dengan TTL.

## Batas keamanan

- Kode akses: 10 karakter acak, TTL 15 menit, hanya hash SHA-256 yang disimpan.
- Endpoint lookup publik dibatasi 10 percobaan/menit per IP; endpoint pembuatan
  kode hanya untuk pemilik pesanan.
- Email, kode akses, dan OTP tidak pernah ditulis ke log, audit, atau pesan
  error.
- Mailbox (pusat maupun per akun) hanya dibaca (IMAP `EXAMINE`) dan koneksi
  wajib TLS minimal 1.2 dengan verifikasi hostname. App password mailbox
  disimpan terenkripsi dengan `CredentialCipher` dan didekripsi hanya di memori
  saat lookup.
- Mailbox per akun dibatasi fail-closed ke allowlist Gmail
  (`imap.gmail.com`/`imap.googlemail.com`, port 993). Nilai host dari record
  admin divalidasi ulang oleh factory reader; host lain tidak pernah didial.
- Penerima OTP dibandingkan sebagai alamat email hasil parse `net/mail`, exact
  case-insensitive. Display name, komentar, dan substring pada header mentah
  tidak pernah dianggap cocok. Header envelope yang malformed atau berisi lebih
  dari satu alamat diabaikan.
- Deadline absolut dipasang pada socket setelah dial; pembatalan context
  menutup koneksi sehingga `LOGIN`/`EXAMINE`/`SEARCH`/`FETCH`/`LOGOUT` tidak
  menggantung setelah timeout.
- OTP yang ditampilkan adalah kode terbaru yang dialamatkan ke email akun dan
  masuk dalam lookback window; tidak ada pembacaan seluruh isi mailbox ke klien.
