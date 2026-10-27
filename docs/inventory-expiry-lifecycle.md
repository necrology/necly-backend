# Masa aktif akun dan slot sharing (NEC-137)

Dokumen ini adalah kontrak backend untuk job expiry, pengingat, dan aksi admin
masa aktif. Ruang lingkupnya hanya lifecycle; harga tidak diubah di sini.

## Aturan data

- `inventory_accounts.expires_at` adalah masa aktif akun/provider.
- `inventory_slots.access_expires_at` adalah masa aktif milik satu pembeli.
- Satu akun maksimal melayani 5 slot (`sharing_limit`), tetapi expiry selalu
  per slot. Expiry satu pembeli tidak boleh mengubah `access_expires_at` atau
  status slot pembeli lain pada akun yang sama.

## Job harian

`InventoryLifecycleService` berjalan sekali saat startup lalu setiap 24 jam:

1. Slot dengan `access_expires_at <= now` dan status bukan `expired`/`released`
   diubah menjadi `expired`.
2. Akun berstatus `active` dengan `expires_at <= now` diubah menjadi `expired`.
3. Slot milik akun yang expired **tidak** diubah oleh langkah 2. Stok berhenti
   karena katalog, checkout, dan delivery hanya memakai akun `active` yang
   `expires_at`-nya masih menutup durasi paket.
4. Pengingat H-7 dan H-1 ditulis ke tabel `notifications` yang sudah ada:
   - slot dikirim ke pembeli (`orders.user_id`, fallback `reserved_by`);
   - akun inventaris dikirim ke seluruh admin aktif.
5. Semua transisi dicatat di `audit_logs` (`inventory.slot.expired`,
   `inventory.account.expired`).

Job bersifat idempoten:

- update transisi memakai predikat status/expiry, sehingga run kedua tidak
  mengubah baris lagi;
- pengingat memakai `notifications.dedupe_key` unik berisi target, penerima,
  window H-N, dan tanggal expiry. Run ulang atau dua instance bersamaan tidak
  membuat notifikasi ganda.

## Aksi admin

| Endpoint | Body | Perilaku |
|---|---|---|
| `POST /api/v1/admin/inventory/:id/extend` | `{"duration_days":30}` atau `{"expires_at":"2026-12-31T23:59:59Z"}` | `duration_days` dihitung dari `max(now, expiry lama)`. `expires_at` eksplisit wajib maju. Akun `expired` kembali `active`; akun `inactive` tetap `inactive`. |
| `POST /api/v1/admin/inventory/:id/deactivate` | — | Status akun menjadi `inactive`; baris dan riwayat order tetap ada. |
| `POST /api/v1/admin/inventory/slots/:slotId/extend` | `{"duration_days":30}` | `access_expires_at = max(now, expiry lama) + duration_days`. Ditolak bila hasilnya melewati `inventory_accounts.expires_at`. |
| `POST /api/v1/admin/inventory/slots/:slotId/deactivate` | — | Status slot menjadi `inactive`; baris tidak dihapus. |

Semua perubahan masa aktif/status di atas dicatat ke `audit_logs` dengan actor
admin dan metadata `previous_expiry`/`new_expiry` atau status sebelum-sesudah.
Endpoint `PATCH /api/v1/admin/inventory/:id` yang sudah ada juga mencatat
perubahan `expires_at`/`status`.

### Error validasi penting

| HTTP | `code` | Arti |
|---|---|---|
| 400 | `invalid_input` | Durasi di luar 1–3650 hari, atau body ambigu/kosong. |
| 422 | `slot_exceeds_account_expiry` | Masa aktif slot akan melewati masa aktif akun. |
| 422 | `account_expiry_not_extended` | `expires_at` baru tidak memindahkan masa aktif ke depan. |
| 409 | `inventory_account_unavailable` | Akun tidak aktif atau sudah expired. |
| 409 | `inventory_slot_unavailable` | Slot sudah `released` dan tidak dapat diubah. |

## Batas penjualan

Slot baru (termasuk hasil checkout) tidak boleh melewati masa aktif akun:

- katalog hanya menghitung slot `available` dari akun `active` yang sisa masa
  aktifnya menutup durasi paket;
- checkout mengunci slot dengan predikat yang sama;
- delivery menolak pengiriman dengan `error_code`
  `inventory_account_expiry_too_short` bila durasi pesanan akan melewati
  `inventory_accounts.expires_at`.

## Migration

`023_inventory_expiry_lifecycle.sql` menambahkan
`notifications.dedupe_key` (nullable, unik) dan index
`inventory_accounts(expires_at)`. Keduanya aditif dan aman dijalankan ulang.
