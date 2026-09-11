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
