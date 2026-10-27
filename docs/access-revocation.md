# Pembatalan langganan dan pencabutan akses (NEC-141)

Dokumen ini menjelaskan cara menghentikan langganan pelanggan yang akunnya
sudah pernah dikirim. Tujuannya bukan hanya menandai pesanan sebagai
`cancelled`, tetapi memastikan pelanggan tidak bisa lagi masuk ke platform yang
dibeli (misalnya ChatGPT Plus).

## Batasan penting

Menghapus slot atau mengubah status pesanan di Necly **tidak** otomatis
mengeluarkan pelanggan dari platform pihak ketiga. Pelanggan sudah memegang
email dan kata sandi akun dari email pengiriman. Karena itu, pencabutan akses
harus disertai:

1. mengganti kata sandi akun di platform;
2. memilih **logout dari semua perangkat / revoke semua sesi** di platform;
3. memasukkan kata sandi baru ke Necly agar kredensial tersimpan ikut berubah
   dan slot tidak dijual kembali dengan kata sandi lama.

Necly tidak memiliki API resmi ke setiap platform (ChatGPT, Netflix, dan
sebagainya), sehingga langkah 1–2 dilakukan operator di dashboard platform.
Langkah 3 dan pencabutan akses internal dilakukan oleh endpoint di bawah.

## Alur admin

1. Buka **Dashboard → Pesanan → detail pesanan**.
2. Klik **Batalkan & cabut akses**.
3. Panel menampilkan akun yang sudah dikirim beserta jumlah pelanggan lain yang
   masih aktif pada akun yang sama.
4. Ganti kata sandi akun di platform, lalu logout semua perangkat.
5. Isi kata sandi baru untuk setiap akun, centang konfirmasi rotasi, dan bila
   akun dipakai bersama centang konfirmasi dampak shared account.
6. Pilih status akhir `cancelled` atau `refunded`, isi alasan, lalu konfirmasi.

Setelah berhasil:

- status pesanan berubah dan pembayaran disinkronkan seperti transisi status
  biasa;
- seluruh slot pesanan dilepas ke stok (`available`) dan masa aksesnya
  dikosongkan;
- kode akses OTP yang pernah dibuat langsung tidak berlaku;
- halaman pembeli menampilkan bahwa akses sudah dicabut;
- audit `admin.order.access_revoked` mencatat jumlah akun dan slot tanpa
  kredensial.

### Akun sharing

Satu akun bisa dipakai sampai lima pelanggan. Mengganti kata sandi akun
tersebut mengeluarkan **semua** pelanggan di akun yang sama. Panel menampilkan
jumlah pelanggan lain dan mewajibkan konfirmasi. Operator harus mengirim kata
sandi baru kepada pelanggan lain yang masih aktif. Pengiriman ulang otomatis
belum termasuk dalam increment ini.

Jika akun tidak dipakai pelanggan lain, rotasi hanya mengeluarkan pelanggan
yang dibatalkan.

## Kontrak API

Semua endpoint hanya untuk admin (`role=admin`).

### `GET /api/v1/admin/orders/:id/revocation-impact`

```json
{
  "accounts": [
    {
      "inventory_account_id": "uuid",
      "label": "Akun utama",
      "account_email": "akun@example.test",
      "other_active_users": 2
    }
  ]
}
```

- Hanya akun yang delivery-nya sudah berstatus `sent` dan slotnya masih aktif.
- `accounts: []` berarti belum ada kredensial yang dikirim; pembatalan tidak
  memerlukan rotasi kata sandi.
- Response tidak pernah memuat kata sandi atau ciphertext.

### `POST /api/v1/admin/orders/:id/revoke-access`

```json
{
  "status": "cancelled",
  "reason": "Pelanggan berhenti berlangganan",
  "provider_rotation_confirmed": true,
  "shared_impact_acknowledged": true,
  "new_passwords": {
    "inventory-account-uuid": "kata-sandi-baru"
  }
}
```

- `status` hanya `cancelled` atau `refunded`.
- Untuk pesanan yang sudah dikirim, `provider_rotation_confirmed` wajib
  `true` dan `new_passwords` wajib berisi kata sandi baru untuk setiap akun pada
  impact.
- `shared_impact_acknowledged` wajib `true` bila ada
  `other_active_users > 0`.
- Response sukses:

```json
{
  "order": { "id": "uuid", "status": "cancelled" },
  "accounts": [],
  "released_slots": 1
}
```

Kode kesalahan yang stabil:

| HTTP | Code | Arti |
|---|---|---|
| 400 | `invalid_input` | Status, alasan, atau payload tidak valid. |
| 400 | `new_password_required` | Ada akun terkirim tanpa kata sandi baru. |
| 404 | `not_found` | Pesanan tidak ditemukan. |
| 409 | `invalid_transition` | Status awal tidak bisa menuju status akhir. |
| 409 | `provider_rotation_required` | Pesanan sudah dikirim; konfirmasi rotasi belum diberikan. |
| 409 | `shared_access_impact` | Akun masih dipakai pelanggan lain; konfirmasi dampak belum diberikan. |
| 503 | `credential_rotation_unavailable` | Layanan penyimpanan kata sandi tidak tersedia. |

Perubahan status biasa (`PATCH /api/v1/admin/orders/:id/status`) menolak
`cancelled`/`refunded` untuk pesanan yang delivery-nya sudah `sent` dengan kode
`provider_rotation_required`. Jalur ini sengaja tidak boleh melewati rotasi
kata sandi.

## Pertahanan runtime

- `inventory_slots` yang `released`, `revoked`, atau `expired` tidak lagi
  dianggap sebagai material pengiriman.
- `POST /api/v1/account-otp/lookup` memeriksa ulang status pesanan, status
  delivery, status slot, dan masa akses setiap kali kode dipakai. Kode yang
  dibuat sebelum pembatalan langsung ditolak dengan pesan generik
  `otp_access_invalid`.
- Halaman detail pesanan pembeli tidak menampilkan akses akun untuk pesanan
  `cancelled`/`refunded`.

## Residual risk

- Perubahan kata sandi di platform tidak selalu mengakhiri sesi yang sedang
  aktif. Operator wajib memakai opsi logout semua perangkat/revoke sessions.
- Necly tidak dapat memverifikasi apakah langkah di platform benar-benar
  dijalankan. Konfirmasi operator dicatat di audit, dan kata sandi baru disimpan
  terenkripsi.
- Pelanggan lain pada akun sharing harus diberi kata sandi baru secara manual
  sampai fitur notifikasi ulang otomatis tersedia.
