package crawler

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"leadforge/internal/fixsrv"
)

func TestGuardBlocksPrivate(t *testing.T) {
	g := Guard{}
	for _, raw := range []string{
		"http://localhost/admin",
		"http://127.0.0.1/",
		"http://10.0.0.5/",
		"http://192.168.1.1/",
		"http://172.16.0.9/",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
		"file:///etc/passwd",
		"ftp://example.com/x",
		"javascript:alert(1)",
	} {
		u, err := g.ValidateURL(raw)
		if err != nil {
			continue // rejected at parse/scheme level — good
		}
		if _, err := g.CheckHost(context.Background(), u.Host); err == nil {
			t.Errorf("expected %s to be blocked", raw)
		}
	}
}

func TestGuardAllowsPublic(t *testing.T) {
	g := Guard{}
	// 8.8.8.8 = Google Public DNS (documentation-safe literal)
	ips, err := g.CheckHost(context.Background(), "8.8.8.8")
	if err != nil || len(ips) == 0 {
		t.Fatalf("public IP should be allowed: %v", err)
	}
}

func TestGuardAllowPrivateOverride(t *testing.T) {
	g := Guard{AllowPrivate: true}
	if _, err := g.CheckHost(context.Background(), "127.0.0.1"); err != nil {
		t.Fatalf("override should allow loopback: %v", err)
	}
}

const fixtureHTML = `<!DOCTYPE html><html><head>
<title>PT Maju Konstruksi - Kontraktor Jakarta</title>
<meta name="description" content="Kontraktor umum dan renovasi gedung di Jakarta.">
<meta property="og:site_name" content="PT Maju Konstruksi">
</head><body>
<h1>Selamat datang</h1>
<p>Hubungi kami di <a href="mailto:info@majukonstruksi.co.id">info@majukonstruksi.co.id</a>
atau telepon 0812-3456-7890. Kantor: Jl. Sudirman No. 1, Jakarta.</p>
<a href="https://wa.me/6281234567890">WhatsApp</a>
<a href="https://www.linkedin.com/company/maju-konstruksi">LinkedIn</a>
<a href="https://instagram.com/majukonstruksi">IG</a>
<script src="/wp-content/themes/x/app.js"></script>
<address>Jl. Sudirman No. 1, Jakarta Selatan</address>
</body></html>`

func TestExtractFixture(t *testing.T) {
	ex := Extract("https://majukonstruksi.co.id/", []byte(fixtureHTML))
	if ex.CompanyName != "PT Maju Konstruksi" {
		t.Errorf("company = %q", ex.CompanyName)
	}
	if !strings.Contains(ex.Description, "Kontraktor") {
		t.Errorf("desc = %q", ex.Description)
	}
	if len(ex.Emails) == 0 || ex.Emails[0] != "info@majukonstruksi.co.id" {
		t.Errorf("emails = %v", ex.Emails)
	}
	if len(ex.Phones) == 0 {
		t.Errorf("no phones extracted")
	}
	if ex.PrimaryWhatsApp() == "" {
		t.Errorf("no whatsapp extracted")
	}
	if ex.Socials["linkedin"] == "" || ex.Socials["instagram"] == "" {
		t.Errorf("socials = %v", ex.Socials)
	}
	found := false
	for _, tech := range ex.Technologies {
		if tech.Name == "WordPress" {
			if tech.Evidence == "" {
				t.Errorf("missing evidence for WordPress")
			}
			found = true
		}
	}
	if !found {
		t.Errorf("technologies = %v", ex.Technologies)
	}
	if !strings.Contains(ex.Address, "Sudirman") {
		t.Errorf("address = %q", ex.Address)
	}
}

func TestExtractParenAreaCode(t *testing.T) {
	ex := Extract("https://x.co.id/", []byte(`<html><body><p>Telp: (021) 555-8899</p></body></html>`))
	if len(ex.Phones) == 0 {
		t.Fatalf("no phones from parenthesized area code")
	}
}

func fixCfg() SiteConfig {
	return SiteConfig{MaxPages: 5, MaxDepth: 2, Workers: 2, DomainConc: 2,
		Timeout: 15 * time.Second, MaxBody: 5 << 20, UserAgent: "LeadForgeBot/1.0 test", Stop: &atomic.Bool{}}
}

func TestFixtureRedirectCanonical(t *testing.T) {
	fx, err := fixsrv.New()
	if err != nil {
		t.Skipf("no loopback: %v", err)
	}
	defer fx.Close()
	g := Guard{AllowPrivate: true}
	res := CrawlSite(context.Background(), g, nil, fx.URL+"/redirect", fixCfg())
	if res.Pages == 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	if !strings.HasSuffix(res.FinalURL, "/co/acme") {
		t.Fatalf("final url = %q", res.FinalURL)
	}
}

func TestFixtureFlakyRetries(t *testing.T) {
	fx, err := fixsrv.New()
	if err != nil {
		t.Skipf("no loopback: %v", err)
	}
	defer fx.Close()
	g := Guard{AllowPrivate: true}
	res := CrawlSite(context.Background(), g, nil, fx.URL+"/flaky", fixCfg())
	if res.Pages == 0 {
		t.Fatalf("429 should be retried to success: %v", res.Errors)
	}
	if len(res.Data.Emails) == 0 {
		t.Fatalf("no emails after retry")
	}
}

func TestFixture404NoRetryStorm(t *testing.T) {
	fx, err := fixsrv.New()
	if err != nil {
		t.Skipf("no loopback: %v", err)
	}
	defer fx.Close()
	g := Guard{AllowPrivate: true}
	t0 := time.Now()
	res := CrawlSite(context.Background(), g, nil, fx.URL+"/missing", fixCfg())
	if res.Pages != 0 {
		t.Fatalf("404 should yield no pages")
	}
	if time.Since(t0) > 10*time.Second {
		t.Fatalf("404 retried too long: %v", time.Since(t0))
	}
}

func TestFixtureJSShellDetected(t *testing.T) {
	fx, err := fixsrv.New()
	if err != nil {
		t.Skipf("no loopback: %v", err)
	}
	defer fx.Close()
	g := Guard{AllowPrivate: true}
	res := CrawlSite(context.Background(), g, nil, fx.URL+"/js", fixCfg())
	if !res.Data.JSRequired {
		t.Fatalf("JS shell not detected (quality=%d)", QualityScore(res.Data, res.Pages))
	}
}

func TestFixtureSmartDiscovery(t *testing.T) {
	fx, err := fixsrv.New()
	if err != nil {
		t.Skipf("no loopback: %v", err)
	}
	defer fx.Close()
	g := Guard{AllowPrivate: true}
	res := CrawlSite(context.Background(), g, nil, fx.URL+"/co/beta", fixCfg())
	if res.Pages < 2 {
		t.Fatalf("expected homepage + contact/about, got %d (%v)", res.Pages, res.Errors)
	}
	if len(res.Data.Emails) == 0 {
		t.Fatalf("no emails discovered across pages")
	}
	if q := QualityScore(res.Data, res.Pages); q < 40 {
		t.Fatalf("quality too low: %d", q)
	}
}
