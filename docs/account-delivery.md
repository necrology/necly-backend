# Pengiriman akun setelah approval (W17)

Dokumen ini adalah kontrak backend untuk alur "Setujui & kirim akun" pada
Increment 2. Frontend W18 memakai endpoint dan status di bawah; QA/Security
memakai bagian batas keamanan sebagai acuan verifikasi.

## Trigger

Kredensial hanya dikirim setelah administrator menyetujui pesanan. Dua jalur
yang setara:

1. `PATCH /api/v1/admin/orders/:id/status` dengan `{"status":"processing"}`.
   Transisi `paid`/`fulfilling` → `processing` memicu pengiriman.
2. `POST /api/v1/admin/orders/:id/approve-delivery` — aksi eksplisit
   "Setujui & kirim akun". Endpoint ini melakukan transisi yang sama bila pesanan
   masih `paid`/`fulfilling`, lalu menjalankan pengiriman. Aman dipanggil ulang.

Webhook pembayaran **tidak** mengirim kredensial. Order yang baru `paid` tetap
menunggu approval admin.

## Format kredensial inventaris

`inventory_accounts.encrypted_credentials` menyimpan salah satu format:

```
username:password
```

atau JSON (disarankan, dapat memuat catatan per akun):

```json
{
  "username": "akun@contoh.test",
  "password": "kata-sandi",
  "instructions": "Gunakan profil 1 dan jangan logout."
}
```

Kunci `email` diterima sebagai alias `username`. Baris pertama format teks
dipisah pada tanda `:` pertama; baris berikutnya menjadi instruksi tambahan.
Placeholder lama seperti `BELUM DIISI ...` sengaja ditolak dengan status
`credential_invalid`, bukan dikirim ke pembeli.

## Status pengiriman

Satu baris `order_deliveries` per `order_item`. Status:

| Status | Arti |
|---|---|
| `pending` | Baris dibuat, belum ada percobaan kirim. |
| `sending` | Sedang dikirim; klaim baris bersifat atomik. Lease 5 menit. |
| `sent` | Email berhasil dikirim. Replay/retry tidak mengirim ulang. |
| `failed` | Gagal; `error_code` menjelaskan penyebab dan dapat di-retry admin. |

`error_code` yang stabil (tanpa kredensial/alamat email):

- `buyer_email_unverified`
- `buyer_unavailable`
- `mailer_disabled`
- `credential_cipher_unavailable`
- `inventory_unavailable`
- `inventory_account_unavailable`
- `credential_invalid`
- `email_failed`
- `persistence_failed`

Kegagalan tidak mengubah status pesanan: pesanan tetap `processing` (atau status
setelahnya) dan dapat di-retry.

## Endpoint admin

- `GET /api/v1/admin/orders/:id` — detail pesanan, menyertakan array
  `deliveries` (tanpa kredensial).
- `GET /api/v1/admin/orders/:id/deliveries` — daftar status pengiriman.
- `POST /api/v1/admin/orders/:id/approve-delivery` — setujui dan kirim.
- `POST /api/v1/admin/orders/:id/deliveries/:deliveryId/retry` — ulangi satu
  pengiriman. Baris `sent` dikembalikan tanpa mengirim email baru.

Semua endpoint memakai middleware admin-only (`role=admin`).

## Isi email

Email dikirim ke `users.email` pembeli yang sudah diverifikasi (`email_verified_at`
terisi, akun aktif) dan berisi:

- nomor pesanan, produk, durasi, jumlah;
- untuk setiap akun unik: label, username, password, catatan bila ada;
- instruksi penggunaan dan ajakan menghubungi dukungan dengan nomor pesanan.

Satu email per order item. Bila beberapa slot memakai akun sharing yang sama,
kredensial akun tersebut hanya dicantumkan sekali.

## Pembatalan setelah akun dikirim

Pesanan yang sudah dikirim tidak boleh dibatalkan lewat perubahan status biasa.
Gunakan alur **Batalkan & cabut akses** yang mewajibkan rotasi kata sandi akun di
platform, melepas slot, dan membatalkan kode OTP yang pernah dibuat. Kontrak
lengkap ada di [access-revocation.md](access-revocation.md).

## Batas keamanan dan residual risk

- Hanya `OrderDeliveryService` yang memanggil `CredentialCipher.Decrypt`, dan
  hanya setelah status pesanan `processing` atau lebih tinggi.
- Kredensial tidak pernah ditulis ke response API, log, audit metadata, pesan
  error, atau tabel `order_deliveries`.
- Pengiriman email memakai transport SMTP yang mewajibkan STARTTLS. Bila mailer
  tidak aktif (mis. `SMTP_HOST` kosong di produksi), pengiriman gagal tertutup
  dengan `mailer_disabled`; tidak ada fallback log yang membocorkan isi email.
- Idempotensi dijaga oleh `UNIQUE(order_item_id)` dan klaim status atomik.
  Retry/replay tidak menggandakan email yang sudah `sent`.
- Residual risk: email berisi kredensial dalam bentuk teks. Ini permintaan
  eksplisit Project Owner; risiko dikurangi dengan TLS, akses admin, audit, dan
  idempotensi. Secure-link dapat dipertimbangkan pada increment berikutnya.
- Residual risk: crash tepat setelah SMTP menerima pesan tetapi sebelum baris
  ditandai `sent` dapat menyebabkan satu email duplikat saat retry setelah lease.
  Ini adalah batas at-least-once SMTP yang tidak dapat dihilangkan tanpa
  idempotency di sisi penyedia email.
