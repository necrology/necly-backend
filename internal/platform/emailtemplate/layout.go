package emailtemplate

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// LayoutConfig holds the configurable parts of the email layout.
type LayoutConfig struct {
	// CompanyName is the sender name shown in header and footer.
	CompanyName string
	// SupportURL is the base URL for "Laporan Kendala" links.
	SupportURL string
	// PreheaderText is the hidden preview text shown in email clients.
	PreheaderText string
	// CurrentYear for the footer copyright.
	CurrentYear int
}

// DefaultLayoutConfig returns sensible defaults.
func DefaultLayoutConfig() LayoutConfig {
	return LayoutConfig{
		CompanyName:   "NECLY Services",
		SupportURL:    "https://necly.example",
		PreheaderText: "NECLY Services - Informasi pesanan dan akses akun",
		CurrentYear:   time.Now().Year(),
	}
}

// LayoutData holds the dynamic content injected into the layout.
type LayoutData struct {
	// Title is the email subject/title shown in the header.
	Title string
	// Greeting is the personalized greeting (e.g., "Halo Budi,").
	Greeting string
	// IntroText is the introductory paragraph before the main content.
	IntroText string
	// CodeCard is optional: if non-nil, renders the large access code card.
	CodeCard *CodeCardData
	// SummaryTable is optional: if non-nil, renders the order/account summary table.
	SummaryTable *SummaryTableData
	// StepsTable is optional: if non-nil, renders the numbered steps table.
	StepsTable *StepsTableData
	// WarningBox is optional: if non-nil, renders the yellow warning box.
	WarningBox *WarningBoxData
	// HelpBox is optional: if non-nil, renders the help/support box.
	HelpBox *HelpBoxData
	// FooterNote is optional additional text before the standard footer.
	FooterNote string
}

// CodeCardData holds the access code card content.
type CodeCardData struct {
	// Label is the title of the card (e.g., "Kode Akses OTP").
	Label string
	// Code is the actual access code to display.
	Code string
	// ExpiresAt is the expiration time (will be formatted as WIB).
	ExpiresAt time.Time
	// ExpiresLabel is optional custom label for expiration (default: "Berlaku sampai").
	ExpiresLabel string
}

// SummaryTableData holds the summary table rows.
type SummaryTableData struct {
	// Rows are key-value pairs displayed in a two-column table.
	Rows []SummaryRow
}

// SummaryRow is a single row in the summary table.
type SummaryRow struct {
	Label string
	Value string
}

// StepsTableData holds the numbered steps for the "Cara mengambil OTP" section.
type StepsTableData struct {
	// Title is the section title (e.g., "Cara mengambil OTP").
	Title string
	// Steps are the individual steps, each with a number, title, and description.
	Steps []Step
}

// Step is a single step in the steps table.
type Step struct {
	Number        int
	Title         string
	Description   string
	ButtonLabel   string // optional: if set, renders a button link
	ButtonURL     string // optional: URL for the button
	TextLinkLabel string // optional: if set, renders a text link below button
	TextLinkURL   string // optional: URL for the text link
	IsWarning     bool   // if true, renders with warning styling (yellow background)
}

// WarningBoxData holds the yellow warning box content.
type WarningBoxData struct {
	Title   string
	Message string
}

// HelpBoxData holds the help/support box content.
type HelpBoxData struct {
	Title       string
	Message     string
	ButtonLabel string
	ButtonURL   string
	Note        string
}

// RenderLayout renders the complete HTML email using the shared layout.
// It returns the HTML string and any error (e.g., template execution failure).
func RenderLayout(config LayoutConfig, data LayoutData) (string, error) {
	var b strings.Builder

	// Preheader (hidden)
	b.WriteString(`<div style="display:none;font-size:1px;color:#f4f6fb;line-height:1px;max-height:0;max-width:0;opacity:0;overflow:hidden;mso-hide:all;">`)
	b.WriteString(html.EscapeString(config.PreheaderText))
	b.WriteString("</div>\n")

	// Outer wrapper table (full width, background color)
	b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="background-color:#f4f6fb;padding:24px 12px;">`)
	b.WriteString("<tr><td align=\"center\">\n")

	// Inner card table (max 600px, white background)
	b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="max-width:600px;background-color:#ffffff;border-radius:8px;overflow:hidden;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">`)

	// Header
	b.WriteString("<tr><td style=\"padding:24px 24px 16px 24px;background-color:#4f46e5;\">")
	b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%"><tr>`)
	b.WriteString(`<td style="color:#ffffff;font-size:20px;font-weight:600;letter-spacing:-0.02em;">`)
	b.WriteString(html.EscapeString(config.CompanyName))
	b.WriteString("</td>")
	b.WriteString(`<td align="right" style="color:#c7d2fe;font-size:12px;font-weight:500;">`)
	b.WriteString(html.EscapeString(data.Title))
	b.WriteString("</td>")
	b.WriteString("</tr></table>")
	b.WriteString("</td></tr>\n")

	// Content area
	b.WriteString("<tr><td style=\"padding:24px;\">")

	// Greeting
	if data.Greeting != "" {
		b.WriteString(`<p style="margin:0 0 16px 0;color:#111827;font-size:16px;line-height:1.5;">`)
		b.WriteString(html.EscapeString(data.Greeting))
		b.WriteString("</p>\n")
	}

	// Intro text
	if data.IntroText != "" {
		b.WriteString(`<p style="margin:0 0 24px 0;color:#111827;font-size:15px;line-height:1.6;">`)
		b.WriteString(html.EscapeString(data.IntroText))
		b.WriteString("</p>\n")
	}

	// Code Card
	if data.CodeCard != nil {
		b.WriteString(renderCodeCard(data.CodeCard))
		b.WriteString("\n")
	}

	// Summary Table
	if data.SummaryTable != nil {
		b.WriteString(renderSummaryTable(data.SummaryTable))
		b.WriteString("\n")
	}

	// Steps Table
	if data.StepsTable != nil {
		b.WriteString(renderStepsTable(data.StepsTable))
		b.WriteString("\n")
	}

	// Warning Box
	if data.WarningBox != nil {
		b.WriteString(renderWarningBox(data.WarningBox))
		b.WriteString("\n")
	}

	// Help Box
	if data.HelpBox != nil {
		b.WriteString(renderHelpBox(data.HelpBox))
		b.WriteString("\n")
	}

	// Footer Note
	if data.FooterNote != "" {
		b.WriteString(`<p style="margin:16px 0 0 0;color:#6b7280;font-size:13px;line-height:1.6;">`)
		b.WriteString(html.EscapeString(data.FooterNote))
		b.WriteString("</p>\n")
	}

	b.WriteString("</td></tr>\n")

	// Footer
	b.WriteString("<tr><td style=\"padding:16px 24px 24px 24px;border-top:1px solid #e5e7eb;\">")
	b.WriteString(`<p style="margin:0 0 8px 0;color:#6b7280;font-size:12px;line-height:1.5;">`)
	b.WriteString(fmt.Sprintf("© %d %s. Seluruh hak cipta dilindungi.", config.CurrentYear, html.EscapeString(config.CompanyName)))
	b.WriteString("</p>")
	b.WriteString(`<p style="margin:0;color:#9ca3af;font-size:11px;line-height:1.5;">`)
	b.WriteString("Email ini dikirim otomatis dan tidak menerima balasan. ")
	b.WriteString(fmt.Sprintf("Jika Anda mengalami kendala, kunjungi <a href=\"%s/laporan-kendala\" style=\"color:#4f46e5;text-decoration:underline;\">Laporan Kendala</a>.", html.EscapeString(config.SupportURL)))
	b.WriteString("</p>")
	b.WriteString("</td></tr>\n")

	b.WriteString("</table>\n")
	b.WriteString("</td></tr></table>\n")

	// Embedded <style> for media queries and resets only
	b.WriteString(`<style>
@media (max-width:600px) {
	/* Reduce card padding on mobile */
	.card-padding { padding: 16px !important; }
	/* Keep code readable on mobile */
	.code-block { font-size: 22px !important; letter-spacing: 2px !important; }
	/* Reduce header padding */
	.header-padding { padding: 16px 16px 12px 16px !important; }
	/* Reduce content padding */
	.content-padding { padding: 16px !important; }
	/* Reduce footer padding */
	.footer-padding { padding: 12px 16px 16px 16px !important; }
	/* Stack summary table cells */
	.summary-cell { display: block; width: 100% !important; padding: 8px 0 !important; border-bottom: 1px solid #e5e7eb !important; }
	.summary-cell:last-child { border-bottom: none !important; }
	/* Stack steps */
	.step-cell { display: block; width: 100% !important; }
	.step-number { margin-bottom: 4px !important; }
}
</style>`)

	return b.String(), nil
}

// renderCodeCard renders the large access code card.
func renderCodeCard(card *CodeCardData) string {
	var b strings.Builder
	expiresLabel := "Berlaku sampai"
	if card.ExpiresLabel != "" {
		expiresLabel = card.ExpiresLabel
	}
	wib := card.ExpiresAt.In(time.FixedZone("WIB", 7*3600)).Format("02 Jan 2006 15:04")

	b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="margin-bottom:24px;">`)
	b.WriteString("<tr><td class=\"card-padding\" style=\"padding:24px;background-color:#eef2ff;border-radius:8px;border:1px solid #e0e7ff;\">")
	// Label
	b.WriteString(`<p style="margin:0 0 12px 0;color:#3730a3;font-size:14px;font-weight:600;text-transform:uppercase;letter-spacing:0.05em;">`)
	b.WriteString(html.EscapeString(card.Label))
	b.WriteString("</p>")
	// Code block
	b.WriteString(`<div style="background-color:#ffffff;border:1px solid #c7d2fe;border-radius:6px;padding:20px;text-align:center;">`)
	b.WriteString(`<code class="code-block" style="font-family:'SF Mono','Fira Code','Fira Mono',Menlo,Consolas,monospace;font-size:28px;font-weight:700;letter-spacing:3px;color:#111827;user-select:all;-webkit-user-select:all;-moz-user-select:all;-ms-user-select:all;display:block;word-break:break-all;">`)
	b.WriteString(html.EscapeString(card.Code))
	b.WriteString("</code>")
	b.WriteString("</div>")
	// Expires
	b.WriteString(fmt.Sprintf(`<p style="margin:12px 0 0 0;color:#6b7280;font-size:13px;"><strong>%s:</strong> %s WIB</p>`, html.EscapeString(expiresLabel), html.EscapeString(wib)))
	b.WriteString("</td></tr></table>")
	return b.String()
}

// renderSummaryTable renders the two-column summary table.
func renderSummaryTable(table *SummaryTableData) string {
	var b strings.Builder
	b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="margin-bottom:24px;border:1px solid #e5e7eb;border-radius:8px;overflow:hidden;">`)
	for i, row := range table.Rows {
		bg := "#ffffff"
		if i%2 == 1 {
			bg = "#f9fafb"
		}
		b.WriteString(fmt.Sprintf("<tr style=\"background-color:%s;\">", bg))
		b.WriteString(`<td class="summary-cell" style="padding:12px 16px;color:#6b7280;font-size:14px;font-weight:500;width:35%;border-bottom:1px solid #e5e7eb;vertical-align:top;">`)
		b.WriteString(html.EscapeString(row.Label))
		b.WriteString("</td>")
		b.WriteString(`<td class="summary-cell" style="padding:12px 16px;color:#111827;font-size:14px;font-weight:400;width:65%;border-bottom:1px solid #e5e7eb;vertical-align:top;word-break:break-word;">`)
		b.WriteString(html.EscapeString(row.Value))
		b.WriteString("</td>")
		b.WriteString("</tr>")
	}
	b.WriteString("</table>")
	return b.String()
}

// renderStepsTable renders the numbered steps table.
func renderStepsTable(table *StepsTableData) string {
	var b strings.Builder
	if table.Title != "" {
		b.WriteString(`<h3 style="margin:0 0 16px 0;color:#111827;font-size:16px;font-weight:600;">`)
		b.WriteString(html.EscapeString(table.Title))
		b.WriteString("</h3>\n")
	}
	b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="margin-bottom:24px;">`)
	for _, step := range table.Steps {
		bgColor := "#ffffff"
		borderColor := "#e5e7eb"
		if step.IsWarning {
			bgColor = "#fffbeb"
			borderColor = "#fde68a"
		}
		b.WriteString(fmt.Sprintf("<tr style=\"background-color:%s;border:1px solid %s;border-radius:8px;\">", bgColor, borderColor))
		// Number circle + title
		b.WriteString(`<td class="step-cell" style="padding:16px;vertical-align:top;width:100%;">`)
		b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%"><tr>`)
		b.WriteString(`<td class="step-number" style="vertical-align:top;width:32px;padding-right:12px;">`)
		b.WriteString(fmt.Sprintf(`<span style="display:inline-flex;align-items:center;justify-content:center;width:28px;height:28px;border-radius:50%%;background-color:#4f46e5;color:#ffffff;font-size:13px;font-weight:700;line-height:1;">%d</span>`, step.Number))
		b.WriteString("</td>")
		b.WriteString(`<td style="vertical-align:top;">`)
		b.WriteString(fmt.Sprintf(`<p style="margin:0 0 4px 0;color:#111827;font-size:15px;font-weight:600;">%s</p>`, html.EscapeString(step.Title)))
		if step.Description != "" {
			b.WriteString(fmt.Sprintf(`<p style="margin:0;color:#374151;font-size:14px;line-height:1.6;">%s</p>`, html.EscapeString(step.Description)))
		}
		// Button
		if step.ButtonLabel != "" && step.ButtonURL != "" {
			b.WriteString(fmt.Sprintf(`<p style="margin:12px 0 0 0;"><a href="%s" style="display:inline-block;padding:10px 20px;background-color:#4f46e5;color:#ffffff;font-size:14px;font-weight:600;text-decoration:none;border-radius:6px;">%s</a></p>`, html.EscapeString(step.ButtonURL), html.EscapeString(step.ButtonLabel)))
		}
		// Text link
		if step.TextLinkLabel != "" && step.TextLinkURL != "" {
			b.WriteString(fmt.Sprintf(`<p style="margin:8px 0 0 0;color:#4f46e5;font-size:13px;"><a href="%s" style="color:#4f46e5;text-decoration:underline;">%s</a></p>`, html.EscapeString(step.TextLinkURL), html.EscapeString(step.TextLinkLabel)))
		}
		b.WriteString("</td>")
		b.WriteString("</tr></table>")
		b.WriteString("</td>")
		b.WriteString("</tr>")
	}
	b.WriteString("</table>")
	return b.String()
}

// renderWarningBox renders the yellow warning box.
func renderWarningBox(box *WarningBoxData) string {
	var b strings.Builder
	b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="margin-bottom:24px;background-color:#fffbeb;border:1px solid #fde68a;border-radius:8px;">`)
	b.WriteString("<tr><td style=\"padding:16px;\">")
	b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%"><tr>`)
	b.WriteString(`<td style="vertical-align:top;width:28px;"><svg width="20" height="20" viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M10 1.5L18.5 17.5H1.5L10 1.5Z" stroke="#b45309" stroke-width="1.5"/><path d="M10 7.5V11.5M10 14H10.01" stroke="#b45309" stroke-width="1.5" stroke-linecap="round"/></svg></td>`)
	b.WriteString(`<td style="vertical-align:top;padding-left:12px;">`)
	if box.Title != "" {
		b.WriteString(fmt.Sprintf(`<p style="margin:0 0 4px 0;color:#b45309;font-size:14px;font-weight:600;">%s</p>`, html.EscapeString(box.Title)))
	}
	b.WriteString(fmt.Sprintf(`<p style="margin:0;color:#92400e;font-size:14px;line-height:1.6;">%s</p>`, html.EscapeString(box.Message)))
	b.WriteString("</td></tr></table>")
	b.WriteString("</td></tr></table>")
	return b.String()
}

// renderHelpBox renders the help/support box.
func renderHelpBox(box *HelpBoxData) string {
	var b strings.Builder
	b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="margin-bottom:24px;background-color:#f0fdf4;border:1px solid #86efac;border-radius:8px;">`)
	b.WriteString("<tr><td style=\"padding:16px;\">")
	if box.Title != "" {
		b.WriteString(fmt.Sprintf(`<p style="margin:0 0 8px 0;color:#166534;font-size:14px;font-weight:600;">%s</p>`, html.EscapeString(box.Title)))
	}
	if box.Message != "" {
		b.WriteString(fmt.Sprintf(`<p style="margin:0 0 12px 0;color:#15803d;font-size:14px;line-height:1.6;">%s</p>`, html.EscapeString(box.Message)))
	}
	if box.ButtonLabel != "" && box.ButtonURL != "" {
		b.WriteString(fmt.Sprintf(`<p style="margin:0 0 8px 0;"><a href="%s" style="display:inline-block;padding:10px 20px;background-color:#059669;color:#ffffff;font-size:14px;font-weight:600;text-decoration:none;border-radius:6px;">%s</a></p>`, html.EscapeString(box.ButtonURL), html.EscapeString(box.ButtonLabel)))
	}
	if box.Note != "" {
		b.WriteString(fmt.Sprintf(`<p style="margin:0;color:#166534;font-size:13px;">%s</p>`, html.EscapeString(box.Note)))
	}
	b.WriteString("</td></tr></table>")
	return b.String()
}
