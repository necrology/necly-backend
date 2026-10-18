package service

import "errors"

var (
	ErrInvalidInput     = errors.New("Input tidak valid")
	ErrUnauthorized     = errors.New("Tidak terotorisasi")
	ErrForbidden        = errors.New("Akses ditolak")
	ErrNotFound         = errors.New("Data tidak ditemukan")
	ErrConflict         = errors.New("Terjadi konflik data")
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
	ErrAccountRequired   = errors.New("Buat akun inventaris untuk produk ini terlebih dahulu sebelum menambah slot")
	ErrInvalidTransition = errors.New("Perubahan status pesanan tidak valid")
)
