package emailtemplate

import (
	"fmt"
	"strings"
	"time"
)

// AccessCodeEmailInput holds data required to render the "Kode akses OTP" email.
type AccessCodeEmailInput struct {
	CustomerName  string
	OrderNumber   string
	ProductName   string
	DurationName  string
	AccountEmails []string
	AccessCode    string
	ExpiresAt     time.Time
	FrontendURL   string
	CompanyName   string
}

// DeliveryEmailInput holds data required to render the "Akses pesanan" email.
type DeliveryEmailInput struct {
	CustomerName        string
	OrderNumber         string
	ProductName         string
	DurationName        string
	Quantity            int
	AccessExpiresAt     time.Time
	Accounts            []DeliveryAccountView
	FrontendURL         string
	CompanyName         string
	AccessCode          string
	AccessCodeExpiresAt time.Time
}

// ReplacementEmailInput holds data required to render the "Akun pengganti" email.
type ReplacementEmailInput struct {
	CustomerName    string
	OrderNumber     string
	ProductName     string
	DurationName    string
	Quantity        int
	AccessExpiresAt time.Time
	Accounts        []DeliveryAccountView
	Reason          string
	FrontendURL     string
	CompanyName     string
}

// DeliveryAccountView holds account credentials for email rendering.
type DeliveryAccountView struct {
	Label        string
	Username     string
	Password     string
	Instructions string
}

// RenderAccessCodeEmail renders the "Kode akses OTP" email HTML.
func RenderAccessCodeEmail(input AccessCodeEmailInput) (string, error) {
	company := input.CompanyName
	if company == "" {
		company = "NECLY Services"
	}
	supportURL := input.FrontendURL
	if supportURL == "" {
		supportURL = "https://necly.example"
	}

	layoutCfg := LayoutConfig{
		CompanyName:   company,
		SupportURL:    supportURL,
		PreheaderText: fmt.Sprintf("Kode akses OTP untuk pesanan %s — %s", input.OrderNumber, input.ProductName),
		CurrentYear:   time.Now().Year(),
	}

	emailsListStr := strings.Join(input.AccountEmails, ", ")

	summaryRows := []SummaryRow{
		{Label: "Nomor pesanan", Value: input.OrderNumber},
		{Label: "Produk", Value: input.ProductName},
		{Label: "Durasi", Value: input.DurationName},
		{Label: "Email akun", Value: emailsListStr},
	}

	otpURL := supportURL + "/otp"
	laporanURL := supportURL + "/laporan-kendala"

	steps := []Step{
		{
			Number:      1,
			Title:       "Login ke " + input.ProductName + " pakai email + kata sandi akun",
			Description: "Buka " + input.ProductName + " (misalnya chatgpt.com), masukkan email akun dan kata sandi dari kartu kredensial di email ini, lalu tekan Log in. WAJIB: JANGAN pilih Continue with Google / Sign in with Apple / SSO, karena jalur itu tidak mengirim kode OTP.",
			IsWarning:   true,
		},
		{
			Number:      2,
			Title:       "Tunggu form kode OTP muncul di " + input.ProductName,
			Description: "Setelah kata sandi diterima, " + input.ProductName + " menampilkan permintaan kode verifikasi. JANGAN tutup halaman atau tab ini — form OTP harus tetap terbuka sebelum Anda lanjut.",
		},
		{
			Number:        3,
			Title:         "Buka halaman Ambil OTP Necly dan masukkan email akun + kode akses",
			Description:   "Baru setelah form OTP muncul, buka halaman Ambil OTP Necly. Masukkan email akun dan kode akses Necly 10 karakter dari kotak besar di atas. Kode akses Necly BUKAN kode OTP 6 digit.",
			ButtonLabel:   "Buka Halaman Ambil OTP",
			ButtonURL:     otpURL,
			TextLinkLabel: otpURL,
			TextLinkURL:   otpURL,
		},
		{
			Number:      4,
			Title:       "Ambil kode OTP-nya, kembali ke " + input.ProductName + ", tempel di form",
			Description: "Necly membaca kode OTP 6 digit dari email akun Anda dan menampilkannya di halaman Ambil OTP. Salin kode 6 digit itu, kembali ke tab " + input.ProductName + " yang masih terbuka, lalu tempel di kolom verifikasi. Login akun selesai.",
		},
		{
			Number:      5,
			Title:       "Ambil ulang jika kode belum muncul",
			Description: "Jika kode tidak muncul setelah 60 detik, tekan tombol ambil ulang (resend) di " + input.ProductName + ", lalu ulangi Langkah 3 dan 4.",
		},
	}

	layoutData := LayoutData{
		Title:     "Kode Akses untuk Ambil OTP",
		Greeting:  fmt.Sprintf("Halo %s,", input.CustomerName),
		IntroText: "Anda meminta kode akses untuk mengambil OTP login akun langganan Anda. Gunakan kode di bawah ini bersama email akun di halaman Ambil OTP Necly.",
		MandatoryBox: &WarningBoxData{
			Title: "WAJIB — OTP hanya terbit kalau Anda login pakai KATA SANDI",
			Message: "Kode OTP baru dikirim ke email akun setelah Anda memasukkan email dan kata sandi akun di form login biasa platform.\n" +
				"• Login lewat \"Continue with Google\" / \"Sign in with Apple\" / SSO lain TIDAK mengirim kode OTP.\n" +
				"• Urutan benar: isi email + kata sandi di platform, tekan Log in, tunggu email kode verifikasi, lalu kembali ke halaman Ambil OTP Necly.",
		},
		CodeCard: &CodeCardData{
			Label:        "Kode Akses Necly (bukan OTP 6 digit)",
			Code:         input.AccessCode,
			ExpiresAt:    input.ExpiresAt,
			ExpiresLabel: "Masa berlaku",
		},
		SummaryTable: &SummaryTableData{Rows: summaryRows},
		StepsTable: &StepsTableData{
			Title: "Cara mengambil OTP langkah demi langkah",
			Steps: steps,
		},
		HelpBox: &HelpBoxData{
			Title:       "Pusat Laporan Kendala",
			Message:     "Jika Anda mengalami kesulitan atau kode tidak kunjung masuk, buat laporan kendala dengan menyertakan nomor pesanan Anda.",
			ButtonLabel: "Buka Laporan Kendala",
			ButtonURL:   laporanURL,
			Note:        "Pastikan Anda menyertakan nomor pesanan " + input.OrderNumber + " dalam laporan.",
		},
		FooterNote: "Jangan bagikan kode akses ini kepada siapa pun. Jika Anda tidak meminta kode ini, abaikan email ini.",
	}

	return RenderLayout(layoutCfg, layoutData)
}

// RenderDeliveryEmail renders the "Akses pesanan" email HTML.
func RenderDeliveryEmail(input DeliveryEmailInput) (string, error) {
	company := input.CompanyName
	if company == "" {
		company = "NECLY Services"
	}
	supportURL := input.FrontendURL
	if supportURL == "" {
		supportURL = "https://necly.example"
	}

	layoutCfg := LayoutConfig{
		CompanyName:   company,
		SupportURL:    supportURL,
		PreheaderText: fmt.Sprintf("Akses pesanan %s (%s) sudah aktif", input.OrderNumber, input.ProductName),
		CurrentYear:   time.Now().Year(),
	}

	summaryRows := []SummaryRow{
		{Label: "Nomor pesanan", Value: input.OrderNumber},
		{Label: "Produk", Value: input.ProductName},
		{Label: "Durasi", Value: input.DurationName},
		{Label: "Jumlah akses", Value: fmt.Sprintf("%d", input.Quantity)},
		{Label: "Masa akses", Value: "sampai " + formatTanggal(input.AccessExpiresAt)},
	}

	// Build credential cards from accounts (one card per account).
	// The title carries the product name, while Subtitle carries the account
	// label so multiple accounts in one order stay distinguishable.
	productLabel := strings.ToUpper(strings.TrimSpace(input.ProductName))
	if productLabel == "" {
		productLabel = "LANGGANAN"
	}
	var credentialCards []CredentialCardData
	for _, acc := range input.Accounts {
		card := CredentialCardData{
			Title:    fmt.Sprintf("KREDENSIAL AKUN %s", productLabel),
			Subtitle: acc.Label,
			Rows: []CredentialRow{
				{Label: "Email akun", Value: acc.Username, Monospace: true},
				{Label: "Kata sandi", Value: acc.Password, Monospace: true},
			},
			Note: "Pakai email dan kata sandi ini untuk login ke platform langganan. Kode akses Necly di atas dipakai di halaman Ambil OTP, bukan di sini.",
		}
		if acc.Instructions != "" {
			card.Note += " Catatan: " + acc.Instructions
		}
		credentialCards = append(credentialCards, card)
	}

	laporanURL := supportURL + "/laporan-kendala"

	// Build mandatory box: OTP hanya terbit kalau pelanggan login pakai email + kata sandi,
	// bukan lewat Google/Apple/SSO. Tampil selalu: kewajiban login kata sandi berlaku bahkan saat kode akses nonaktif.
	mandatoryBox := &WarningBoxData{
		Title: "WAJIB — Login pakai KATA SANDI, form OTP harus terbuka dulu",
		Message: "Kode OTP hanya terbit kalau Anda login memakai email dan kata sandi akun di form login biasa platform.\n" +
			"• Login lewat \"Continue with Google\" / \"Sign in with Apple\" / SSO lain TIDAK mengirim kode OTP.\n" +
			"• Urutan benar: login pakai kata sandi di platform → tunggu form kode OTP muncul → BARU buka halaman Ambil OTP Necly → salin kode dari email akun → kembali ke platform dan tempel di form.",
	}

	// Build steps table: minimal 5 langkah
	steps := []Step{
		{
			Number:      1,
			Title:       "Login ke " + input.ProductName + " pakai email + kata sandi akun",
			Description: "Buka " + input.ProductName + " (misalnya chatgpt.com), masukkan email akun dan kata sandi dari kartu kredensial di atas, lalu tekan Log in. WAJIB: JANGAN pilih Continue with Google / Sign in with Apple / SSO, karena jalur itu tidak mengirim kode OTP.",
			IsWarning:   true,
		},
		{
			Number:      2,
			Title:       "Tunggu form kode OTP muncul di " + input.ProductName,
			Description: "Setelah kata sandi diterima, " + input.ProductName + " menampilkan permintaan kode verifikasi. JANGAN tutup halaman atau tab ini — form OTP harus tetap terbuka sebelum Anda lanjut.",
		},
		{
			Number:        3,
			Title:         "Buka halaman Ambil OTP Necly dan masukkan email akun + kode akses",
			Description:   "Baru setelah form OTP muncul, buka halaman Ambil OTP Necly. Masukkan email akun dan kode akses Necly 10 karakter dari kartu biru di atas. Kode akses Necly BUKAN kode OTP 6 digit.",
			ButtonLabel:   "Buka Halaman Ambil OTP",
			ButtonURL:     supportURL + "/otp",
			TextLinkLabel: supportURL + "/otp",
			TextLinkURL:   supportURL + "/otp",
		},
		{
			Number:      4,
			Title:       "Ambil kode OTP-nya, kembali ke " + input.ProductName + ", tempel di form",
			Description: "Necly membaca kode OTP 6 digit dari email akun Anda dan menampilkannya di halaman Ambil OTP. Salin kode 6 digit itu, kembali ke tab " + input.ProductName + " yang masih terbuka, lalu tempel di kolom verifikasi. Login akun selesai.",
		},
		{
			Number:      5,
			Title:       "Ambil ulang jika kode belum muncul",
			Description: "Jika kode tidak muncul setelah 60 detik, tekan tombol ambil ulang (resend) di " + input.ProductName + ", lalu ulangi Langkah 3 dan 4.",
		},
	}

	stepsTable := &StepsTableData{
		Title: "Cara mengambil OTP langkah demi langkah",
		Steps: steps,
	}

	// Build code card: hanya tampilkan kalau AccessCode diisi
	var codeCard *CodeCardData
	if input.AccessCode != "" {
		codeCard = &CodeCardData{
			Label:        "Kode Akses Necly (bukan OTP 6 digit)",
			Code:         input.AccessCode,
			ExpiresAt:    input.AccessCodeExpiresAt,
			ExpiresLabel: "Masa berlaku",
		}
	}

	layoutData := LayoutData{
		Title:           "Akses Pesanan Aktif",
		Greeting:        fmt.Sprintf("Halo %s,", input.CustomerName),
		IntroText:       "Akses untuk pesanan Anda sudah aktif. Berikut detail pesanan dan akun langganan yang dapat Anda gunakan.",
		MandatoryBox:    mandatoryBox,
		CodeCard:        codeCard,
		SummaryTable:    &SummaryTableData{Rows: summaryRows},
		CredentialCards: credentialCards,
		StepsTable:      stepsTable,
		WarningBox: &WarningBoxData{
			Title:   "PERINGATAN PENTING: Jangan ubah kata sandi / profil",
			Message: "Jangan mengubah kata sandi, email, atau profil akun karena akun ini digunakan bersama sesuai paket yang dibeli. Pelanggaran dapat menyebabkan pencabutan garansi akses.",
		},
		HelpBox: &HelpBoxData{
			Title:       "Pusat Laporan Kendala",
			Message:     "Jika Anda mengalami kendala saat login atau menggunakan akun, laporkan segera melalui halaman Laporan Kendala.",
			ButtonLabel: "Buka Laporan Kendala",
			ButtonURL:   laporanURL,
			Note:        "Sertakan selalu nomor pesanan " + input.OrderNumber + " saat membuat laporan.",
		},
		// FooterNote no longer carries credentials; keep a generic note if desired.
		FooterNote: "Jangan bagikan kredensial akun ini kepada siapa pun. Jika Anda tidak meminta akses ini, abaikan email ini.",
	}

	return RenderLayout(layoutCfg, layoutData)
}

// RenderReplacementEmail renders the "Akun pengganti" email HTML.
func RenderReplacementEmail(input ReplacementEmailInput) (string, error) {
	company := input.CompanyName
	if company == "" {
		company = "NECLY Services"
	}
	supportURL := input.FrontendURL
	if supportURL == "" {
		supportURL = "https://necly.example"
	}

	layoutCfg := LayoutConfig{
		CompanyName:   company,
		SupportURL:    supportURL,
		PreheaderText: fmt.Sprintf("Akun pengganti untuk pesanan %s — %s", input.OrderNumber, input.ProductName),
		CurrentYear:   time.Now().Year(),
	}

	summaryRows := []SummaryRow{
		{Label: "Nomor pesanan", Value: input.OrderNumber},
		{Label: "Produk", Value: input.ProductName},
		{Label: "Durasi", Value: input.DurationName},
		{Label: "Alasan penggantian", Value: input.Reason},
		{Label: "Masa akses baru", Value: "sampai " + formatTanggal(input.AccessExpiresAt)},
	}

	// Build credential cards from accounts (one card per account).
	// Title carries the product name; Subtitle carries the account label so
	// multiple replacement accounts stay distinguishable.
	productLabel := strings.ToUpper(strings.TrimSpace(input.ProductName))
	if productLabel == "" {
		productLabel = "LANGGANAN"
	}
	var credentialCards []CredentialCardData
	for _, acc := range input.Accounts {
		card := CredentialCardData{
			Title:    fmt.Sprintf("KREDENSIAL AKUN %s (PENGGANTI)", productLabel),
			Subtitle: acc.Label,
			Rows: []CredentialRow{
				{Label: "Email akun", Value: acc.Username, Monospace: true},
				{Label: "Kata sandi", Value: acc.Password, Monospace: true},
			},
			Note: "Pakai email dan kata sandi ini untuk login ke platform langganan. Kode akses Necly dipakai di halaman Ambil OTP, bukan di sini.",
		}
		if acc.Instructions != "" {
			card.Note += " Catatan: " + acc.Instructions
		}
		credentialCards = append(credentialCards, card)
	}

	laporanURL := supportURL + "/laporan-kendala"

	layoutData := LayoutData{
		Title:           "Akun Pengganti",
		Greeting:        fmt.Sprintf("Halo %s,", input.CustomerName),
		IntroText:       "Akun untuk pesanan Anda telah diganti karena masa aktif akun sebelumnya berakhir atau keluhan Anda disetujui. Tidak ada tagihan baru untuk penggantian ini.",
		SummaryTable:    &SummaryTableData{Rows: summaryRows},
		CredentialCards: credentialCards,
		WarningBox: &WarningBoxData{
			Title:   "PERINGATAN PENTING: Jangan ubah kata sandi / profil",
			Message: "Jangan mengubah kata sandi, email, atau profil akun pengganti ini.",
		},
		HelpBox: &HelpBoxData{
			Title:       "Pusat Laporan Kendala",
			Message:     "Jika Anda mengalami kendala dengan akun pengganti, laporkan melalui Laporan Kendala.",
			ButtonLabel: "Buka Laporan Kendala",
			ButtonURL:   laporanURL,
			Note:        "Sertakan nomor pesanan " + input.OrderNumber + ".",
		},
		// FooterNote no longer carries credentials.
		FooterNote: "Jangan bagikan kredensial akun pengganti ini kepada siapa pun.",
	}

	return RenderLayout(layoutCfg, layoutData)
}
