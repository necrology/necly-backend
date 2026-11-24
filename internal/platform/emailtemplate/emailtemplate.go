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
			Number:        1,
			Title:         "Buka halaman Ambil OTP",
			Description:   "Buka halaman Ambil OTP di situs Necly melalui tombol atau tautan di bawah ini.",
			ButtonLabel:   "Buka Halaman Ambil OTP",
			ButtonURL:     otpURL,
			TextLinkLabel: otpURL,
			TextLinkURL:   otpURL,
		},
		{
			Number:      2,
			Title:       "WAJIB — Masukkan email akun dan kata sandi di form login biasa",
			Description: "Buka platform layanan yang bersangkutan (misalnya chatgpt.com), lalu ketik email akun dan kata sandi di form login biasa, kemudian tekan Log in. Langkah ini tidak boleh dilewati: tanpa login memakai kata sandi, kode OTP tidak akan pernah dikirim ke email akun.",
			IsWarning:   true,
		},
		{
			Number:      3,
			Title:       "JANGAN login pakai Google / Apple / SSO",
			Description: "Jangan pernah memilih \"Continue with Google\", \"Sign in with Apple\", atau login via SSO pihak ketiga. Jalur tersebut tidak mengirim kode OTP ke email akun.",
			IsWarning:   true,
		},
		{
			Number:      4,
			Title:       "Tunggu email kode verifikasi masuk (10–60 detik)",
			Description: "Setelah kata sandi diterima platform, sistem akan mengirim kode OTP ke email akun. Tunggu selama 10 hingga 60 detik sampai email masuk.",
		},
		{
			Number:      5,
			Title:       "Kembali ke halaman Ambil OTP",
			Description: "Kembali ke halaman Ambil OTP Necly yang sudah dibuka pada Langkah 1.",
		},
		{
			Number:      6,
			Title:       "Masukkan email akun + kode akses Necly, lalu ambil OTP",
			Description: "Ketik email akun dan salin kode akses Necly dari kotak besar di atas (10 karakter, bukan kode OTP) ke kolom yang tersedia, lalu tekan tombol ambil OTP.",
		},
		{
			Number:      7,
			Title:       "Salin kode OTP 6 digit dan tempel di platform",
			Description: "Kode OTP 6 digit terbaru akan muncul di layar. Salin kode tersebut, lalu tempel di kolom verifikasi platform layanan.",
		},
		{
			Number:      8,
			Title:       "Ambil ulang jika kode belum muncul",
			Description: "Jika kode tidak muncul setelah 60 detik, tekan tombol ambil ulang (resend) di platform layanan, lalu ulangi Langkah 5 dan 6.",
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

	// Build summary table or custom representation for accounts
	// For delivery email, we can include account credentials in the summary or custom text/boxes.
	// Let's create account rows or text.
	var accountsSummary strings.Builder
	for i, acc := range input.Accounts {
		if i > 0 {
			accountsSummary.WriteString("\n\n")
		}
		accountsSummary.WriteString(fmt.Sprintf("Akun %d — %s\n", i+1, acc.Label))
		accountsSummary.WriteString(fmt.Sprintf("Email/username: %s\n", acc.Username))
		accountsSummary.WriteString(fmt.Sprintf("Kata sandi: %s\n", acc.Password))
		if acc.Instructions != "" {
			accountsSummary.WriteString(fmt.Sprintf("Catatan: %s", acc.Instructions))
		}
	}

	laporanURL := supportURL + "/laporan-kendala"

	// Build mandatory box: OTP hanya terbit kalau pelanggan login pakai email + kata sandi,
	// bukan lewat Google/Apple/SSO. Sertakan tautan ke halaman Ambil OTP.
	// Tampil selalu: kewajiban login kata sandi berlaku bahkan saat kode akses nonaktif.
	mandatoryBox := &WarningBoxData{
		Title: "WAJIB — OTP hanya terbit kalau Anda login pakai KATA SANDI",
		Message: "Kode OTP baru dikirim ke email akun setelah Anda memasukkan email dan kata sandi akun di form login biasa platform.\n" +
			"• Login lewat \"Continue with Google\" / \"Sign in with Apple\" / SSO lain TIDAK mengirim kode OTP.\n" +
			"• Urutan benar: isi email + kata sandi di platform, tekan Log in, tunggu email kode verifikasi, lalu kembali ke halaman Ambil OTP Necly.",
	}

	// Build steps table: minimal 5 langkah
	steps := []Step{
		{
			Number:        1,
			Title:         "Buka halaman Ambil OTP",
			Description:   "Buka halaman Ambil OTP di situs Necly melalui tautan di bawah ini.",
			ButtonLabel:   "Buka Halaman Ambil OTP",
			ButtonURL:     supportURL + "/otp",
			TextLinkLabel: supportURL + "/otp",
			TextLinkURL:   supportURL + "/otp",
		},
		{
			Number:      2,
			Title:       "WAJIB — Masukkan email akun dan kata sandi di form login biasa",
			Description: "Buka platform layanan yang bersangkutan (misalnya chatgpt.com), lalu ketik email akun dan kata sandi di form login biasa, kemudian tekan Log in. Langkah ini tidak boleh dilewati: tanpa login memakai kata sandi, kode OTP tidak akan pernah dikirim ke email akun.",
			IsWarning:   true,
		},
		{
			Number:      3,
			Title:       "JANGAN login pakai Google / Apple / SSO",
			Description: "Jangan pernah memilih \"Continue with Google\", \"Sign in with Apple\", atau login via SSO pihak ketiga. Jalur tersebut tidak mengirim kode OTP ke email akun.",
			IsWarning:   true,
		},
		{
			Number:      4,
			Title:       "Tunggu email kode verifikasi masuk (10–60 detik)",
			Description: "Setelah kata sandi diterima platform, sistem akan mengirim kode OTP ke email akun. Tunggu selama 10 hingga 60 detik sampai email masuk.",
		},
		{
			Number:      5,
			Title:       "Kembali ke halaman Ambil OTP",
			Description: "Kembali ke halaman Ambil OTP Necly yang sudah dibuka pada Langkah 1.",
		},
		{
			Number:      6,
			Title:       "Masukkan email akun + kode akses Necly",
			Description: "Ketik email akun dan salin kode akses Necly dari email ini ke kolom yang tersedia, lalu tekan tombol ambil OTP.",
		},
		{
			Number:      7,
			Title:       "Salin kode OTP 6 digit dan tempel di platform",
			Description: "Kode OTP 6 digit terbaru akan muncul di layar. Salin kode tersebut, lalu tempel di kolom verifikasi platform layanan.",
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
		Title:        "Akses Pesanan Aktif",
		Greeting:     fmt.Sprintf("Halo %s,", input.CustomerName),
		IntroText:    "Akses untuk pesanan Anda sudah aktif. Berikut detail pesanan dan akun langganan yang dapat Anda gunakan.",
		MandatoryBox: mandatoryBox,
		CodeCard:     codeCard,
		SummaryTable: &SummaryTableData{Rows: summaryRows},
		StepsTable:   stepsTable,
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
		FooterNote: accountsSummary.String(),
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

	var accountsSummary strings.Builder
	for i, acc := range input.Accounts {
		if i > 0 {
			accountsSummary.WriteString("\n\n")
		}
		accountsSummary.WriteString(fmt.Sprintf("Akun Pengganti %d — %s\n", i+1, acc.Label))
		accountsSummary.WriteString(fmt.Sprintf("Email/username: %s\n", acc.Username))
		accountsSummary.WriteString(fmt.Sprintf("Kata sandi: %s\n", acc.Password))
		if acc.Instructions != "" {
			accountsSummary.WriteString(fmt.Sprintf("Catatan: %s", acc.Instructions))
		}
	}

	laporanURL := supportURL + "/laporan-kendala"

	layoutData := LayoutData{
		Title:        "Akun Pengganti",
		Greeting:     fmt.Sprintf("Halo %s,", input.CustomerName),
		IntroText:    "Akun untuk pesanan Anda telah diganti karena masa aktif akun sebelumnya berakhir atau keluhan Anda disetujui. Tidak ada tagihan baru untuk penggantian ini.",
		SummaryTable: &SummaryTableData{Rows: summaryRows},
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
		FooterNote: accountsSummary.String(),
	}

	return RenderLayout(layoutCfg, layoutData)
}
