package lead

import "testing"

func TestNormalizePhoneID(t *testing.T) {
	cases := []struct {
		in      string
		display string
		e164    string
	}{
		{"081296052010", "+62 8129-6052-010", "+6281296052010"},
		{"+6281296052010", "+62 8129-6052-010", "+6281296052010"},
		{"6281296052010", "+62 8129-6052-010", "+6281296052010"},
		{"0812-9605-2010", "+62 8129-6052-010", "+6281296052010"},
		{"(021) 555-1234", "+62 2155-5123-4", "+62215551234"},
		{"", "", ""},
		{"abc", "", ""},
		{"123", "", ""}, // too short
	}
	for _, c := range cases {
		d, e := NormalizePhone(c.in)
		if d != c.display || e != c.e164 {
			t.Errorf("NormalizePhone(%q) = (%q,%q), want (%q,%q)", c.in, d, e, c.display, c.e164)
		}
	}
}

func TestNormalizeDomain(t *testing.T) {
	cases := map[string]string{
		"WWW.Contoh.CO.ID":  "contoh.co.id",
		"https://x.com":     "",
		"example.com:8080":  "example.com",
		"example.com/":      "",
		"localhost":         "localhost",
		"localhost:8099":    "localhost",
		"not a domain":      "",
		"sub.example.co.id": "sub.example.co.id",
	}
	for in, want := range cases {
		if got := NormalizeDomain(in); got != want {
			t.Errorf("NormalizeDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeWebsiteKeepsPort(t *testing.T) {
	w, d := NormalizeWebsite("http://127.0.0.1:8099/acme.html")
	if d != "127.0.0.1" {
		t.Errorf("domain = %q", d)
	}
	if w != "http://127.0.0.1:8099/acme.html" {
		t.Errorf("website = %q", w)
	}
}

func TestNormalizeWebsite(t *testing.T) {
	w, d := NormalizeWebsite("PT-Maju.co.id/")
	if d != "pt-maju.co.id" {
		t.Errorf("domain = %q", d)
	}
	if w != "https://pt-maju.co.id" {
		t.Errorf("website = %q", w)
	}
	if w2, d2 := NormalizeWebsite("ftp://x.com"); w2 != "" || d2 != "" {
		t.Errorf("ftp should be rejected, got %q %q", w2, d2)
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got := NormalizeEmail("  Admin@Contoh.ID "); got != "admin@contoh.id" {
		t.Errorf("got %q", got)
	}
	if got := NormalizeEmail("not-an-email"); got != "" {
		t.Errorf("bad email accepted: %q", got)
	}
}

func TestNormalizeCandidateKeepsOriginals(t *testing.T) {
	n := NormalizeCandidate("  PT Maju Konstruksi ", "majukonstruksi.co.id",
		"INFO@majukonstruksi.co.id", "0812-3456-789", "", "indonesia", "dki jakarta", "jakarta",
		"Jl. Sudirman No. 1", "Construction")
	if n.Name != "PT Maju Konstruksi" {
		t.Errorf("name = %q", n.Name)
	}
	if n.Domain != "majukonstruksi.co.id" || n.Website != "https://majukonstruksi.co.id" {
		t.Errorf("web = %q %q", n.Website, n.Domain)
	}
	if n.Email != "info@majukonstruksi.co.id" {
		t.Errorf("email = %q", n.Email)
	}
	if n.PhoneE164 != "+628123456789" {
		t.Errorf("phone = %q", n.PhoneE164)
	}
	if n.Province != "DKI Jakarta" {
		t.Errorf("province = %q", n.Province)
	}
}
