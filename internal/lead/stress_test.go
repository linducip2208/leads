package lead

import (
	"sync"
	"testing"
)

// TestConcurrentPureFuncs stresses shared-nothing helpers under goroutines.
// Run with -race in CI (needs cgo; unavailable on this Windows box).
func TestConcurrentPureFuncs(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			NormalizePhone("081296052010")
			NormalizeDomain("WWW.Contoh.CO.ID")
			NormalizeWebsite("contoh.co.id/")
			CanonicalDomain("WWW.Contoh.CO.ID:8080")
			LegalTokens("PT Maju Jaya Indonesia")
			NameSimilarity("PT Maju Jaya Indonesia", "Maju Jaya Indonesia PT")
			PhoneKind("+6281296052010")
			Completeness("PT X", "https://x.co.id", "a@x.co.id", "+621", "addr", "ind", 2, 50)
			DetectOpportunities("Retail", []string{"WooCommerce"}, true)
			_ = i
		}(i)
	}
	wg.Wait()
	if got := NameSimilarity("PT Maju Jaya Indonesia", "Maju Jaya Indonesia PT"); got != 1 {
		t.Fatalf("similarity = %v, want 1", got)
	}
	if got := NameSimilarity("PT Maju Jaya", "CV Berkah Konstruksi"); got >= 0.5 {
		t.Fatalf("similarity = %v, want < 0.5", got)
	}
	if got := CanonicalDomain("WWW.Contoh.CO.ID:8080"); got != "contoh.co.id" {
		t.Fatalf("canonical = %q", got)
	}
}
