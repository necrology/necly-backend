package otpinbox

import (
	"strings"
	"testing"
	"time"
)

// TestParseMessageKeepsLoginCodeWithIndonesianConditionalBoilerplate covers the
// Indonesian conditional connective "jikalau", which appears in provider mail as
// often as "jika". Missing it made the reset-password boilerplate reject a valid
// login code (Security review NEC-206, note 1).
func TestParseMessageKeepsLoginCodeWithIndonesianConditionalBoilerplate(t *testing.T) {
	cases := []string{"jikalau", "jika", "bila", "apabila", "kalau", "kalo", "seandainya", "andaikan", "misalnya"}
	for _, connective := range cases {
		t.Run(connective, func(t *testing.T) {
			raw := strings.Join([]string{
				"To: chatgpt.account@example.test",
				"Subject: Kode masuk ChatGPT sementara Anda",
				"Content-Type: text/html; charset=UTF-8",
				"",
				"<p>Masukkan kode verifikasi sementara ini untuk melanjutkan: " + connective +
					" Anda tidak mencoba masuk, harap reset kata sandi Anda.</p>",
			}, "\r\n")
			// The code sits before the boilerplate; use a real shape with the code.
			raw = strings.Replace(raw, "melanjutkan: "+connective,
				"melanjutkan: 482913. "+connective, 1)

			message, ok := parseMessage([]byte(raw), "chatgpt.account@example.test", time.Now().Add(-time.Hour))
			if !ok {
				t.Fatalf("login code with %q boilerplate was rejected", connective)
			}
			if message.Code != "482913" {
				t.Fatalf("code = %q, want 482913", message.Code)
			}
		})
	}
}
