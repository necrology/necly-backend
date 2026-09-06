package service_test

import (
	"testing"

	"github.com/necly/services/internal/service"
	"github.com/stretchr/testify/assert"
)

func TestServiceErrors_LocalizedMessages(t *testing.T) {
	assert.Equal(t, "Input tidak valid", service.ErrInvalidInput.Error())
	assert.Equal(t, "Tidak terotorisasi", service.ErrUnauthorized.Error())
	assert.Equal(t, "Akses ditolak", service.ErrForbidden.Error())
	assert.Equal(t, "Data tidak ditemukan", service.ErrNotFound.Error())
	assert.Equal(t, "Terjadi konflik data", service.ErrConflict.Error())
	assert.Equal(t, "Email belum diverifikasi. Silakan periksa kotak masuk atau spam Anda.", service.ErrEmailNotVerified.Error())
	assert.Equal(t, "Slot akun langganan tidak mencukupi atau sedang habis", service.ErrOutOfStock.Error())
	assert.Equal(t, "Perubahan status pesanan tidak valid", service.ErrInvalidTransition.Error())
}

func TestVoucherErrors_LocalizedMessages(t *testing.T) {
	assert.Equal(t, "Voucher tidak aktif", service.ErrVoucherInactive.Error())
	assert.Equal(t, "Periode voucher belum dimulai", service.ErrVoucherNotStarted.Error())
	assert.Equal(t, "Masa berlaku voucher telah berakhir", service.ErrVoucherExpired.Error())
	assert.Equal(t, "Total pesanan belum memenuhi syarat minimum voucher", service.ErrVoucherMinimum.Error())
	assert.Equal(t, "Kuota penggunaan voucher telah habis", service.ErrVoucherExhausted.Error())
	assert.Equal(t, "Batas penggunaan voucher per pengguna telah tercapai", service.ErrVoucherUserLimit.Error())
	assert.Equal(t, "Konfigurasi voucher tidak valid", service.ErrVoucherInvalid.Error())
}

func TestWebhookErrors_LocalizedMessages(t *testing.T) {
	assert.Equal(t, "Tanda tangan webhook tidak valid", service.ErrInvalidWebhookSignature.Error())
}
