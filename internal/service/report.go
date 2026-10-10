package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/platform/mailer"
)

// ReportCooldown is how long a visitor must wait between issue reports from the
// same email address (QA baris 57).
const ReportCooldown = 3 * time.Hour

// ReportCategories is the allowlist the public form offers. Anything else is
// rejected so the admin never has to triage free-form category text.
var ReportCategories = []string{"Pembayaran", "Akses layanan", "Pesanan", "Lainnya"}

// ErrReportMismatch is returned when the order number does not exist or was not
// placed with the submitted email. QA baris 55 asks for one combined message
// here so the form cannot be used to probe which order numbers exist.
var ErrReportMismatch = errors.New("nomor pesanan dan email tidak sesuai, cek kembali email yang digunakan untuk memesan, atau no pesanan anda")

// ReportRepository is the persistence slice the report service needs.
type ReportRepository interface {
	CreateIssueReport(ctx context.Context, report *domain.IssueReport) error
	LatestIssueReportByEmail(ctx context.Context, email string) (*domain.IssueReport, error)
	OrderMatchesEmail(ctx context.Context, number, email string) (bool, error)
}

// CooldownGate blocks repeat submissions from the same subject.
type CooldownGate interface {
	BlockedFor(ctx context.Context, subject string) (time.Duration, error)
	Block(ctx context.Context, subject string) error
}

// MailSender is the subset of platform/mailer the services use. Declaring it
// here keeps tests free of SMTP.
type MailSender interface {
	Send(ctx context.Context, msg mailer.Message) error
	Enabled() bool
}

// ValidationError carries per-field messages so the client can highlight the
// exact input that is wrong instead of showing one generic banner.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	// Stable, human-readable fallback for logs and for clients that only read
	// the top-level "error" field.
	return "Data laporan belum lengkap atau tidak valid"
}

// CooldownError reports that the same email filed a report too recently.
type CooldownError struct {
	RetryAfter time.Duration
}

func (e *CooldownError) Error() string {
	minutes := int(e.RetryAfter.Minutes())
	if minutes < 1 {
		minutes = 1
	}
	return fmt.Sprintf("Laporan kendala dari email ini baru saja dikirim. Silakan coba lagi dalam %d menit.", minutes)
}

// ReportInput is the public "Laporan Kendala" payload.
type ReportInput struct {
	Name        string `json:"name"`
	Email       string `json:"email"`
	OrderNumber string `json:"order_number"`
	Category    string `json:"category"`
	Message     string `json:"message"`
}

// ReportService validates, stores and notifies issue reports.
type ReportService struct {
	repo       ReportRepository
	cooldown   CooldownGate
	mail       MailSender
	adminEmail string
	now        func() time.Time
}

// NewReportService wires the report service. A nil cooldown gate, mail sender or
// empty adminEmail all degrade gracefully rather than failing the request.
func NewReportService(repo ReportRepository, cooldown CooldownGate, mail MailSender, adminEmail string) *ReportService {
	return &ReportService{repo: repo, cooldown: cooldown, mail: mail, adminEmail: strings.TrimSpace(adminEmail), now: time.Now}
}

// Create validates the report, enforces the per-email cooldown, stores the row
// and best-effort notifies the admin. The returned report is safe to serialise.
func (s *ReportService) Create(ctx context.Context, input ReportInput, ipAddress string) (*domain.IssueReport, error) {
	name := strings.TrimSpace(input.Name)
	email := normalizeEmail(input.Email)
	orderNumber := strings.ToUpper(strings.TrimSpace(input.OrderNumber))
	category := strings.TrimSpace(input.Category)
	message := strings.TrimSpace(input.Message)

	if fields := validateReportFields(name, email, orderNumber, category, message); len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}

	now := s.now().UTC()

	// Cooldown first: it is the cheapest check and it is what QA baris 57 is
	// about. A repeat within the window is refused before we touch the order.
	if retry, err := s.cooldownRemaining(ctx, email, now); err != nil {
		return nil, err
	} else if retry > 0 {
		return nil, &CooldownError{RetryAfter: retry}
	}

	matches, err := s.repo.OrderMatchesEmail(ctx, orderNumber, email)
	if err != nil {
		return nil, fmt.Errorf("check order ownership: %w", err)
	}
	if !matches {
		return nil, ErrReportMismatch
	}

	report := &domain.IssueReport{
		ID:          uuid.NewString(),
		Name:        name,
		Email:       email,
		OrderNumber: orderNumber,
		Category:    category,
		Message:     message,
		Status:      "new",
		IPAddress:   ipAddress,
		CreatedAt:   now,
	}
	if err := s.repo.CreateIssueReport(ctx, report); err != nil {
		return nil, err
	}

	// Only a stored report starts the window, so a failed attempt never locks
	// the visitor out.
	if err := s.cooldown.Block(ctx, email); err != nil {
		return nil, fmt.Errorf("start report cooldown: %w", err)
	}

	s.notifyAdmin(ctx, report)
	return report, nil
}

// cooldownRemaining returns how long the email must still wait. Redis answers
// first; the newest stored report is the fallback so a Redis flush cannot be
// used to bypass the 3-hour window.
func (s *ReportService) cooldownRemaining(ctx context.Context, email string, now time.Time) (time.Duration, error) {
	if retry, err := s.cooldown.BlockedFor(ctx, email); err == nil && retry > 0 {
		return retry, nil
	}

	latest, err := s.repo.LatestIssueReportByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return 0, nil
		}
		return 0, fmt.Errorf("read latest report: %w", err)
	}
	if latest == nil {
		return 0, nil
	}
	if elapsed := now.Sub(latest.CreatedAt); elapsed < ReportCooldown {
		return ReportCooldown - elapsed, nil
	}
	return 0, nil
}

// notifyAdmin emails the admin address when one is configured. Delivery failure
// must never fail the customer's request: the report is already stored.
func (s *ReportService) notifyAdmin(ctx context.Context, report *domain.IssueReport) {
	if s.adminEmail == "" || s.mail == nil || !s.mail.Enabled() {
		return
	}
	body := strings.Join([]string{
		"Laporan kendala baru dari situs Necly Services.",
		"",
		"Nama        : " + report.Name,
		"Email       : " + report.Email,
		"Nomor pesanan: " + report.OrderNumber,
		"Kategori    : " + report.Category,
		"Dikirim     : " + report.CreatedAt.Format(time.RFC3339),
		"",
		"Keterangan:",
		report.Message,
	}, "\n")

	// Detached from the request so a slow SMTP server cannot hold the response
	// open, and so a client disconnect cannot cancel the notification.
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	go func() {
		defer cancel()
		_ = s.mail.Send(sendCtx, mailer.Message{
			To:      s.adminEmail,
			Subject: "Laporan kendala baru: " + report.OrderNumber,
			Body:    body,
		})
	}()
}

func validateReportFields(name, email, orderNumber, category, message string) map[string]string {
	fields := map[string]string{}
	if name == "" {
		fields["name"] = "Nama wajib diisi."
	}
	if !validEmail(email) {
		fields["email"] = "Masukkan alamat email yang valid, sama dengan email saat memesan."
	}
	if orderNumber == "" {
		fields["order_number"] = "Nomor pesanan wajib diisi, contoh: NCL-XXXX."
	}
	if !isReportCategory(category) {
		fields["category"] = "Pilih salah satu kategori kendala yang tersedia."
	}
	if len([]rune(message)) < 10 {
		fields["message"] = "Jelaskan kendala minimal 10 karakter."
	}
	return fields
}

func isReportCategory(category string) bool {
	for _, allowed := range ReportCategories {
		if strings.EqualFold(allowed, category) {
			return true
		}
	}
	return false
}
