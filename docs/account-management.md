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
| `account_email` | email login akun langganan (read-only, boleh kosong pada akun lama) |
| `mailbox_imap_host` | host mailbox akun; hanya allowlist Gmail, dinormalisasi tanpa port, default `imap.gmail.com` |
| `otp_inbox_configured` | apakah ada mailbox yang bisa dibaca untuk akun ini |
| `otp_inbox_source` | `direct` bila app password akun tersimpan, `central` bila hanya mailbox pusat `OTP_IMAP_*`, `none` bila keduanya tidak tersedia |
| `needs_review` | akun lama di atas limit lima pengguna |
| `sellable` | status aktif dan belum kedaluwarsa |

`encrypted_credentials`, `otp_secret_encrypted`, `mailbox_app_password_encrypted`,
`account_password`, dan `mailbox_app_password` tidak pernah muncul di JSON.

## Create dan update

```json
{
  "product_id": "uuid",
  "account_email": "akun@gmail.com",
  "account_password": "password-login",
  "mailbox_app_password": "app-password-gmail",
  "mailbox_imap_host": "imap.gmail.com",
  "label": "Akun utama",
  "sharing_limit": 5,
  "expires_at": "2026-12-31T23:59:59Z",
  "otp_secret": "JBSWY3DPEHPK3PXP"
}
```

- `account_email` + `account_password` adalah field login terpisah. Keduanya
  disimpan sebagai JSON terenkripsi yang sudah dipahami alur pengiriman akun.
  `account_email` divalidasi dengan `net/mail` (maksimal 254 karakter) dan
  `account_password` minimal satu karakter bila dikirim.
- `credentials` lama tetap diterima. Bila hanya `credentials` yang dikirim,
  perilakunya sama seperti sebelumnya (nilai dienkripsi apa adanya), lalu email
  diisi dari username bila username itu alamat email yang valid.
- `mailbox_app_password` hanya untuk ditulis (write-only). Nilai dienkripsi
  dengan `CredentialCipher` dan dipakai untuk membaca mailbox akun sendiri.
  String kosong eksplisit menghapus app password (kembali ke mailbox pusat);
  field yang tidak dikirim mempertahankan nilai lama.
- `mailbox_imap_host` hanya menerima `imap.gmail.com` atau
  `imap.googlemail.com`, dengan port opsional `:993`. Nilai disimpan sebagai
  hostname tanpa port dan nilai kosong memakai default `imap.gmail.com`.
  Host lain, port selain 993, skema URL, path, alamat IP, dan IPv6 literal
  ditolak `400 invalid_input`. Pembatasan ini juga diterapkan ulang oleh reader
  sebelum dial, sehingga baris database lama atau yang dimanipulasi tidak bisa
  mengarahkan kredensial mailbox ke host lain.
- `otp_secret` hanya untuk ditulis (write-only). Secret base32 divalidasi;
  spasi, huruf kecil, dan padding `=` diterima lalu dinormalisasi.
- Pada `PATCH`, field yang **tidak dikirim** mempertahankan nilai lama. Update
  email saja mempertahankan password dan instructions; update password saja
  mempertahankan email dan instructions. Mengirim `credentials` tetap
  mengganti seluruh blob login seperti kontrak lama.
- `otp_secret: ""` menghapus secret TOTP. `mailbox_app_password: ""` menghapus
  app password mailbox.
- `sharing_limit` di atas 5 ditolak `400 invalid_input`.

Akun lama yang belum punya `account_email` diisi otomatis di memori saat
respons admin dibaca, dengan best effort menyimpan hasilnya ke kolom. Bila satu
baris lama rusak, daftar akun tetap tampil dan baris tersebut dibiarkan kosong.
Email, password, app password, dan ciphertext tidak ditulis ke log atau audit.

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

## Alokasi akun saat pengiriman (Fase 2 backend B)

Semua endpoint di bawah ini hanya untuk admin, tidak pernah mengembalikan
kredensial, dan memakai transaksi dengan row lock agar dua admin tidak dapat
memakai kapasitas akun yang sama secara bersamaan.

### Pilihan akun per item

`GET /api/v1/admin/orders/:id/delivery-options`

Response:

```json
{
  "items": [
    {
      "delivery_id": "uuid",
      "order_item_id": "uuid",
      "product_id": "uuid",
      "product_price_id": "uuid",
      "product_name": "Netflix Premium",
      "duration_name": "1 Bulan",
      "quantity": 1,
      "assigned_account_ids": ["uuid"],
      "accounts": [
        {
          "inventory_account_id": "uuid",
          "label": "Akun utama",
          "status": "active",
          "expires_at": "2026-12-31T23:59:59Z",
          "days_remaining": 70,
          "sharing_limit": 5,
          "assigned_users": 2,
          "used_slots": 2,
          "remaining_slots": 3,
          "used_by_customer": false,
          "assigned_to_item": false,
          "can_assign": true
        }
      ]
    }
  ]
}
```

- Hanya akun `active`, belum kedaluwarsa, dan milik produk item yang muncul.
- `remaining_slots` memakai `min(sharing_limit, 5)` dan tidak menghitung slot
  berstatus `released`.
- `used_by_customer=true` berarti pembeli sudah pernah menerima akun tersebut
  untuk produk yang sama pada pengiriman yang berhasil.
- `can_assign=false` bila akun penuh, sudah dipakai pembeli, atau item quantity
  melebihi kapasitas akun.

### Pilih akun sebelum kirim

`POST /api/v1/admin/orders/:id/deliveries/:deliveryId/assign`

```json
{ "inventory_account_id": "uuid" }
```

- Memindahkan seluruh slot item ke akun terpilih secara atomik dan melepas slot
  dari akun lama.
- Menolak akun produk lain (`409 account_mismatch`), akun nonaktif/kedaluwarsa
  (`409 account_unavailable`), akun penuh (`409 account_full`), akun yang sudah
  dipakai pembeli bila masih ada alternatif (`409 account_used_by_customer`),
  dan semua akun sudah dipakai (`409 no_alternative_account`).
- Tidak mengirim email. Pengiriman tetap dilakukan oleh aksi setujui/kirim.

### Pengiriman otomatis

Saat admin menyetujui pengiriman tanpa memilih akun, backend mempertahankan slot
yang ada hanya bila akunnya aktif, tidak kedaluwarsa, dan belum pernah dikirim
ke pembeli untuk produk yang sama. Bila tidak, slot dipindahkan ke akun eligible
yang belum dipakai. Jika semua akun sudah dipakai, delivery dicatat `failed`
dengan `error_code=no_alternative_account` dan **tidak ada email yang dikirim**.

### Masa akses pembeli

Saat email pengiriman berhasil terkirim, `inventory_slots.activated_at` diisi
waktu kirim dan `access_expires_at` dihitung dari durasi paket pada
`product_durations.days`. Bila durasi tidak tersedia, delivery gagal dengan
`error_code=duration_unavailable`; sistem tidak menebak dari expiry akun.

### Ganti akun expired

`POST /api/v1/admin/orders/:id/deliveries/:deliveryId/replace`

```json
{
  "inventory_account_id": "uuid",
  "reason": "Akun lama expired, pembeli komplain",
  "complaint_approved": false
}
```

- Hanya untuk delivery berstatus `sent`.
- Slot lama harus sudah melewati `access_expires_at`. Bila masih aktif, jawab
  `409 account_not_expired` kecuali `complaint_approved=true` (flag eksplisit
  untuk komplain yang disetujui admin).
- Akun pengganti harus berbeda, aktif, belum kedaluwarsa, milik produk yang
  sama, punya kapasitas, dan belum dipakai pembeli untuk produk tersebut.
- Slot lama ditandai `released`, slot baru dibuat dengan status `reserved` dan
  masa akses baru. Audit `delivery_replacements` menyimpan `replacement_of`,
  daftar slot lama/baru, alasan, dan status email.
- Satu email pengganti dikirim setelah transaksi berhasil. Tidak ada order,
  payment, atau status pembayaran baru.

### Detail pesanan admin

`GET /api/v1/admin/orders/:id` sekarang memuat:

- `payment_status`: status pembayaran terbaru.
- `deliveries[].assigned_accounts[]`: akun terpilih tanpa kredensial, lengkap
  dengan `activated_at`, `access_expires_at`, dan `days_remaining`.
- `deliveries[].replacements[]`: riwayat penggantian beserta status email.
- `deliveries[].status` dan `error_code` tetap seperti kontrak pengiriman lama.
