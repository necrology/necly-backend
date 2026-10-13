package service

import "errors"

var (
	ErrInvalidInput      = errors.New("Input tidak valid")
	ErrUnauthorized      = errors.New("Tidak terotorisasi")
	ErrForbidden         = errors.New("Akses ditolak")
	ErrNotFound          = errors.New("Data tidak ditemukan")
	ErrConflict          = errors.New("Terjadi konflik data")
	ErrEmailNotVerified  = errors.New("Email belum diverifikasi. Silakan periksa kotak masuk atau spam Anda.")
	ErrAlreadyVerified   = errors.New("Email sudah diverifikasi. Silakan masuk menggunakan kata sandi Anda.")
	ErrOutOfStock        = errors.New("Slot akun langganan tidak mencukupi atau sedang habis")
	ErrInvalidTransition = errors.New("Perubahan status pesanan tidak valid")
)
