package webhookstore

import (
	"context"
	"testing"
)

func TestValidateTarget(t *testing.T) {
	for _, target := range []string{
		"http://127.0.0.1:8080/hook",
		"https://127.0.0.1:8443/hook",
		"https://user:pass@example.com/hook",
	} {
		if _, err := ValidateTarget(context.Background(), target, false); err == nil {
			t.Fatalf("expected webhook target rejection: %s", target)
		}
	}
	if _, err := ValidateTarget(context.Background(), "http://127.0.0.1:8080/hook", true); err != nil {
		t.Fatalf("local development exception rejected: %v", err)
	}
}

func TestEventIDIsStableAcrossRetries(t *testing.T) {
	a := EventID("webhook-1", "lead.created", []byte(`{"id":"1"}`))
	b := EventID("webhook-1", "lead.created", []byte(`{"id":"1"}`))
	if a == "" || a != b {
		t.Fatalf("event id must be stable: %q %q", a, b)
	}
	if a == EventID("webhook-1", "lead.updated", []byte(`{"id":"1"}`)) {
		t.Fatal("different event must have different id")
	}
}
