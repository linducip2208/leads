package crawler

import (
	"context"
	"net"
	"testing"
)

func TestGuardRejectsDangerousURLForms(t *testing.T) {
	g := Guard{}
	for _, raw := range []string{
		"file:///etc/passwd",
		"http://user:password@example.com/",
		"http://127.0.0.1/",
		"http://[::1]/",
	} {
		if _, err := g.ValidateAndCheckURL(context.Background(), raw); err == nil {
			t.Fatalf("expected URL to be rejected: %s", raw)
		}
	}
}

func TestGuardBlocksMetadataAndPrivateMappedIPv6(t *testing.T) {
	g := Guard{}
	for _, raw := range []string{"169.254.169.254", "::1", "::ffff:169.254.169.254"} {
		if err := g.CheckIP(net.ParseIP(raw)); err == nil {
			t.Fatalf("expected IP to be blocked: %s", raw)
		}
	}
}

func TestGuardAllowsPublicIP(t *testing.T) {
	if err := (Guard{}).CheckIP(net.ParseIP("1.1.1.1")); err != nil {
		t.Fatalf("public IP rejected: %v", err)
	}
}
