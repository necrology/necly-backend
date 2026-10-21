package service

import "errors"

var (
	ErrInvalidInput = errors.New("Input tidak valid")
	ErrUnauthorized = errors.New("Tidak terotorisasi")
	ErrForbidden    = errors.New("Akses ditolak")
	ErrNotFound     = errors.New("Data tidak ditemukan")
	ErrConflict     = errors.New("Terjadi konflik data")
	// ErrSelfRoleChange and ErrSelfDeactivate protect the admin's own account:
	// an operator must never lock themselves out of the dashboard by accident.
	ErrSelfRoleChange = errors.New("Admin tidak dapat menurunkan peran akunnya sendiri")
	ErrSelfDeactivate = errors.New("Admin tidak dapat menonaktifkan akunnya sendiri")
	ErrSelfDelete     = errors.New("Admin tidak dapat menghapus akunnya sendiri")
	// ErrLastAdmin is the final backstop: no request may leave the system with
	// zero active administrators.
	ErrLastAdmin        = errors.New("Tidak dapat menghapus atau menonaktifkan admin aktif terakhir")
	ErrEmailNotVerified = errors.New("Email belum diverifikasi. Silakan periksa kotak masuk atau spam Anda.")
	ErrAlreadyVerified  = errors.New("Email sudah diverifikasi. Silakan masuk menggunakan kata sandi Anda.")
	// ErrMailUnavailable is returned when production email delivery is not
	// configured. Token-bearing flows fail closed instead of falling back to a
	// log-only sender that would leak activation/reset links into server logs.
	ErrMailUnavailable = errors.New("Layanan email belum tersedia")
	ErrOutOfStock      = errors.New("Slot akun langganan tidak mencukupi atau sedang habis")
	// Slot tidak bisa berdiri sendiri: barisnya wajib menunjuk satu akun induk
	// (inventory_slots.inventory_account_id NOT NULL), jadi akun produk harus
	// dibuat lebih dulu.
	ErrAccountRequired = errors.New("Buat akun inventaris untuk produk ini terlebih dahulu sebelum menambah slot")
	// ErrInventoryAccountUnavailable covers an account that is inactive or past
	// its expiry. It is separate from a full account so the dashboard can tell
	// the operator to reactivate the account instead of raising its limit.
	ErrInventoryAccountUnavailable = errors.New("Akun inventaris tidak aktif atau sudah kedaluwarsa")
	// ErrInventoryAccountMismatch rejects a slot request that names an account
	// belonging to another product.
	ErrInventoryAccountMismatch = errors.New("Akun inventaris bukan milik produk paket ini")
	// ErrSharingLimitExceeded is the capacity guard. It is enforced under a row
	// lock so two concurrent requests cannot exceed sharing_limit together.
	ErrSharingLimitExceeded = errors.New("Jumlah slot melebihi batas sharing akun inventaris")
	// ErrInventoryAccountNeedsReview is the legacy-data guard: an account whose
	// sharing_limit is above the five-user rule keeps its sold slots, but no
	// new slot may be added until an administrator lowers the limit.
	ErrInventoryAccountNeedsReview = errors.New("Akun inventaris melebihi batas 5 pengguna dan perlu ditinjau sebelum menambah slot")
	// ErrOTPNotConfigured means the account has no TOTP secret yet. It is a
	// conflict, not a server error: the admin can set the secret first.
	ErrOTPNotConfigured = errors.New("OTP belum dikonfigurasi untuk akun inventaris ini")
	// ErrOTPRateLimited is returned when one admin asks for too many codes for
	// the same account in a short window. The message never includes the code.
	ErrOTPRateLimited    = errors.New("Terlalu banyak permintaan kode OTP. Coba lagi sebentar lagi")
	ErrInvalidTransition = errors.New("Perubahan status pesanan tidak valid")
	// ErrInvalidPaymentEvent covers a callback whose fields are incomplete or
	// whose state cannot be mapped. It is rejected before any row is mutated.
	ErrInvalidPaymentEvent = errors.New("Notifikasi pembayaran tidak valid")
	// ErrPaymentMismatch means the callback's order, amount, currency or
	// provider payment identity does not match the stored payment. Processing
	// aborts in the same transaction, so a wrong notification cannot change state.
	ErrPaymentMismatch = errors.New("Notifikasi pembayaran tidak cocok dengan pesanan")
	// ErrInventoryAccountInUse protects slots that belong to a live order or
	// are currently reserved by a customer.
	ErrInventoryAccountInUse = errors.New("Akun inventaris masih terhubung ke pesanan atau slot yang dipesan")
	// ErrUnsupportedPaymentChannel is returned when the optional selector value
	// is not in the agreed enum. No order or provider call is created.
	ErrUnsupportedPaymentChannel = errors.New("Metode pembayaran tidak didukung")
	// ErrPaymentChannelNotEligible is reserved for a future server-side
	// method/duration rule. It is not applied until Q1 is approved.
	ErrPaymentChannelNotEligible = errors.New("Metode pembayaran tidak tersedia untuk durasi pesanan ini")
	// ErrOrderNotApproved protects account delivery: credentials may only be
	// decrypted and emailed after an administrator has moved the order to
	// processing (or a later fulfilment state). A paid webhook alone is not an
	// approval.
	ErrOrderNotApproved = errors.New("Pesanan belum disetujui admin untuk pengiriman akun")
	// ErrDeliveryNotFound means the delivery row does not belong to the named
	// order, which also keeps cross-order probing out of the detail endpoint.
	ErrDeliveryNotFound = errors.New("Status pengiriman tidak ditemukan")
)
