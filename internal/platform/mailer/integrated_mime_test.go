package mailer

import (
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"strings"
	"testing"
)

// TestIntegratedRenderProducesTwoReadableParts proves that a Message carrying
// both Body and HTML renders into a MIME message a real client can parse: two
// parts, the HTML part decodes back to the exact input, and the access code
// survives quoted-printable untouched.
func TestIntegratedRenderProducesTwoReadableParts(t *testing.T) {
	m := &smtpMailer{cfg: Config{Host: "smtp.example.com", Port: 587, From: "no-reply@necly.test", UseTLS: true}}
	accessCode := "XK7M9PQ2R4"
	htmlBody := "<html><body><p>Kode akses: <b>" + accessCode + "</b></p>" +
		"<p>Panjang — dengan em dash, emoji 🔐, dan tanda = di tengah.</p></body></html>"

	raw := string(m.render(Message{
		To:      "buyer@example.test",
		Subject: "Kode akses OTP pesanan ORD-1 — berlaku 15 menit",
		Body:    "Kode akses: " + accessCode,
		HTML:    htmlBody,
	}))

	if !strings.Contains(raw, "multipart/alternative") {
		t.Fatalf("expected multipart/alternative, got:\n%s", firstLines(raw, 12))
	}

	// Parse with the same media-type machinery a mail client uses.
	headerEnd := strings.Index(raw, "\r\n\r\n")
	if headerEnd < 0 {
		t.Fatal("no header/body separator")
	}
	header := raw[:headerEnd]
	body := raw[headerEnd+4:]

	var boundary string
	for _, line := range strings.Split(header, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "content-type:") {
			mt, params, err := mime.ParseMediaType(strings.TrimSpace(line[len("content-type:"):]))
			if err != nil {
				t.Fatalf("parse content-type: %v", err)
			}
			if mt != "multipart/alternative" {
				t.Fatalf("media type = %q, want multipart/alternative", mt)
			}
			boundary = params["boundary"]
		}
	}
	if boundary == "" {
		t.Fatal("no boundary parameter")
	}

	reader := multipart.NewReader(strings.NewReader(body), boundary)
	var htmlPart, textPart string
	parts := 0
	for {
		p, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		parts++
		contentType := p.Header.Get("Content-Type")
		decoded, err := io.ReadAll(quotedprintable.NewReader(p))
		if err != nil {
			t.Fatalf("decode part %q: %v", contentType, err)
		}
		switch {
		case strings.HasPrefix(contentType, "text/html"):
			htmlPart = string(decoded)
		case strings.HasPrefix(contentType, "text/plain"):
			textPart = string(decoded)
		}
	}
	if parts != 2 {
		t.Fatalf("part count = %d, want 2", parts)
	}
	if htmlPart != htmlBody {
		t.Fatalf("HTML part did not round-trip.\nwant: %q\ngot:  %q", htmlBody, htmlPart)
	}
	if !strings.Contains(textPart, accessCode) {
		t.Fatalf("plain-text part lost the access code: %q", textPart)
	}
	if !strings.Contains(htmlPart, accessCode) {
		t.Fatalf("HTML part lost the access code")
	}
	if strings.Contains(htmlPart, "=\r\n") && !strings.Contains(htmlBody, "=\r\n") {
		t.Fatalf("soft line breaks leaked into the decoded HTML")
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\r\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\r\n")
}
