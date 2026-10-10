package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/platform/mailer"
)

type fakeReportRepository struct {
	reports  []*domain.IssueReport
	matches  bool
	matchErr error
}

func (r *fakeReportRepository) CreateIssueReport(_ context.Context, report *domain.IssueReport) error {
	copyReport := *report
	r.reports = append(r.reports, &copyReport)
	return nil
}

func (r *fakeReportRepository) LatestIssueReportByEmail(_ context.Context, email string) (*domain.IssueReport, error) {
	var latest *domain.IssueReport
	for _, report := range r.reports {
		if report.Email != email {
			continue
		}
		if latest == nil || report.CreatedAt.After(latest.CreatedAt) {
			latest = report
		}
	}
	if latest == nil {
		return nil, ErrNotFound
	}
	copyReport := *latest
	return &copyReport, nil
}

func (r *fakeReportRepository) OrderMatchesEmail(_ context.Context, _, _ string) (bool, error) {
	return r.matches, r.matchErr
}

// fakeCooldown stands in for the Redis gate. A zero entry means "not blocked".
type fakeCooldown struct {
	blocked map[string]time.Duration
	started map[string]bool
	fail    bool
}

func newFakeCooldown() *fakeCooldown {
	return &fakeCooldown{blocked: map[string]time.Duration{}, started: map[string]bool{}}
}

func (c *fakeCooldown) BlockedFor(_ context.Context, subject string) (time.Duration, error) {
	if c.fail {
		return 0, errors.New("redis unavailable")
	}
	return c.blocked[subject], nil
}

func (c *fakeCooldown) Block(_ context.Context, subject string) error {
	c.started[subject] = true
	return nil
}

// fakeMail records sends so tests can assert on the rendered body.
type fakeMail struct {
	mu      sync.Mutex
	sent    []mailer.Message
	enabled bool
}

func (m *fakeMail) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *fakeMail) Enabled() bool { return m.enabled }

func (m *fakeMail) waitForSend(timeout time.Duration) []mailer.Message {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		count := len(m.sent)
		m.mu.Unlock()
		if count > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mailer.Message(nil), m.sent...)
}

func (m *fakeMail) sentCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func newReportServiceForTest(t *testing.T, repo *fakeReportRepository, cooldown *fakeCooldown, mail *fakeMail, adminEmail string, now time.Time) *ReportService {
	t.Helper()
	service := NewReportService(repo, cooldown, mail, adminEmail)
	service.now = func() time.Time { return now }
	return service
}

func validReportInput() ReportInput {
	return ReportInput{
		Name:        "Budi",
		Email:       " Budi@Example.COM ",
		OrderNumber: "ncl-2026-0001",
		Category:    "Akses layanan",
		Message:     "Akses belum diberikan sejak kemarin malam.",
	}
}

func TestReportCreateRejectsIncompleteFieldsWithPerFieldMessages(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	repo := &fakeReportRepository{matches: true}
	service := newReportServiceForTest(t, repo, newFakeCooldown(), &fakeMail{}, "", now)

	_, err := service.Create(context.Background(), ReportInput{Email: "bukan-email", Category: "Tidak ada"}, "203.0.113.7")

	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("Create() error = %v, want ValidationError", err)
	}
	for _, field := range []string{"name", "email", "order_number", "category", "message"} {
		if validation.Fields[field] == "" {
			t.Errorf("missing message for field %q", field)
		}
	}
	if len(repo.reports) != 0 {
		t.Fatal("an invalid report must not be stored")
	}
}

func TestReportCreateRejectsOrderThatDoesNotBelongToTheEmail(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	repo := &fakeReportRepository{matches: false}
	cooldown := newFakeCooldown()
	service := newReportServiceForTest(t, repo, cooldown, &fakeMail{}, "", now)

	_, err := service.Create(context.Background(), validReportInput(), "203.0.113.7")

	if !errors.Is(err, ErrReportMismatch) {
		t.Fatalf("Create() error = %v, want ErrReportMismatch", err)
	}
	// The QA copy names both inputs, so the message must not single one out.
	if err.Error() != "nomor pesanan dan email tidak sesuai, cek kembali email yang digunakan untuk memesan, atau no pesanan anda" {
		t.Fatalf("unexpected mismatch copy: %q", err.Error())
	}
	if len(repo.reports) != 0 || cooldown.started["budi@example.com"] {
		t.Fatal("a mismatched report must not be stored or start the cooldown")
	}
}

func TestReportCreateStoresNormalisedReportAndStartsCooldown(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	repo := &fakeReportRepository{matches: true}
	cooldown := newFakeCooldown()
	service := newReportServiceForTest(t, repo, cooldown, &fakeMail{}, "", now)

	report, err := service.Create(context.Background(), validReportInput(), "203.0.113.7")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if report.Email != "budi@example.com" {
		t.Errorf("email = %q, want it lower-cased and trimmed", report.Email)
	}
	if report.OrderNumber != "NCL-2026-0001" {
		t.Errorf("order number = %q, want it upper-cased", report.OrderNumber)
	}
	if report.Status != "new" || report.IPAddress != "203.0.113.7" {
		t.Errorf("stored report = %#v", report)
	}
	if len(repo.reports) != 1 {
		t.Fatalf("stored reports = %d, want 1", len(repo.reports))
	}
	if !cooldown.started["budi@example.com"] {
		t.Fatal("a stored report must start the cooldown window")
	}
}

func TestReportCreateRefusesRepeatWhileRedisGateIsSet(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	repo := &fakeReportRepository{matches: true}
	cooldown := newFakeCooldown()
	cooldown.blocked["budi@example.com"] = 2 * time.Hour
	service := newReportServiceForTest(t, repo, cooldown, &fakeMail{}, "", now)

	_, err := service.Create(context.Background(), validReportInput(), "203.0.113.7")

	var blocked *CooldownError
	if !errors.As(err, &blocked) {
		t.Fatalf("Create() error = %v, want CooldownError", err)
	}
	if blocked.RetryAfter != 2*time.Hour {
		t.Errorf("RetryAfter = %v, want 2h", blocked.RetryAfter)
	}
	if len(repo.reports) != 0 {
		t.Fatal("a throttled report must not be stored")
	}
}

// The Redis key is the fast path only. Without it a flushed cache must not
// become a free pass, so the newest stored report of the same email still wins.
func TestReportCreateFallsBackToStoredReportWhenRedisIsEmpty(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	repo := &fakeReportRepository{matches: true, reports: []*domain.IssueReport{
		{ID: "rep-1", Email: "budi@example.com", CreatedAt: now.Add(-time.Hour)},
	}}
	service := newReportServiceForTest(t, repo, newFakeCooldown(), &fakeMail{}, "", now)

	_, err := service.Create(context.Background(), validReportInput(), "203.0.113.7")

	var blocked *CooldownError
	if !errors.As(err, &blocked) {
		t.Fatalf("Create() error = %v, want CooldownError", err)
	}
	if blocked.RetryAfter != 2*time.Hour {
		t.Errorf("RetryAfter = %v, want the 2h left of the 3h window", blocked.RetryAfter)
	}
}

func TestReportCreateAllowsAnotherReportAfterTheCooldownWindow(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	repo := &fakeReportRepository{matches: true, reports: []*domain.IssueReport{
		{ID: "rep-1", Email: "budi@example.com", CreatedAt: now.Add(-4 * time.Hour)},
	}}
	service := newReportServiceForTest(t, repo, newFakeCooldown(), &fakeMail{}, "", now)

	if _, err := service.Create(context.Background(), validReportInput(), "203.0.113.7"); err != nil {
		t.Fatalf("Create() error = %v, want the report to be accepted", err)
	}
	if len(repo.reports) != 2 {
		t.Fatalf("stored reports = %d, want 2", len(repo.reports))
	}
}

func TestReportCreateNotifiesAdminOnlyWhenAnAddressIsConfigured(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)

	withAdmin := &fakeMail{enabled: true}
	service := newReportServiceForTest(t, &fakeReportRepository{matches: true}, newFakeCooldown(), withAdmin, "admin@necly.example", now)
	if _, err := service.Create(context.Background(), validReportInput(), "203.0.113.7"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	sent := withAdmin.waitForSend(2 * time.Second)
	if len(sent) != 1 {
		t.Fatalf("sent emails = %d, want 1", len(sent))
	}
	if sent[0].To != "admin@necly.example" {
		t.Errorf("notification went to %q", sent[0].To)
	}
	if !strings.Contains(sent[0].Body, "NCL-2026-0001") || !strings.Contains(sent[0].Body, "budi@example.com") {
		t.Errorf("notification body is missing the report details: %q", sent[0].Body)
	}

	withoutAdmin := &fakeMail{enabled: true}
	service = newReportServiceForTest(t, &fakeReportRepository{matches: true}, newFakeCooldown(), withoutAdmin, "", now)
	if _, err := service.Create(context.Background(), validReportInput(), "203.0.113.7"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if withoutAdmin.sentCount() != 0 {
		t.Fatal("no notification should be sent when REPORT_ADMIN_EMAIL is empty")
	}
}
