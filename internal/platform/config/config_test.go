package config

import "testing"

func TestProductionRejectsPlaceholderSecrets(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("SESSION_SECRET", "change-me-production-secret-32-bytes")
	t.Setenv("APP_ENCRYPTION_KEY", "independent-production-key-32-bytes")

	if _, err := Load(); err == nil {
		t.Fatal("expected placeholder SESSION_SECRET to be rejected")
	}
}

func TestStrongSecretAcceptsConfiguredValue(t *testing.T) {
	if !strongSecret("a-valid-production-secret-with-entropy-2026") {
		t.Fatal("expected configured secret to pass validation")
	}
	if strongSecret("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatal("expected repeated secret to fail validation")
	}
}
