package crypto

import "testing"

func TestRoundtrip(t *testing.T) {
	enc, err := Encrypt("s3cret", "smtp-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if enc == "smtp-password-123" {
		t.Fatal("not encrypted")
	}
	dec, err := Decrypt("s3cret", enc)
	if err != nil {
		t.Fatal(err)
	}
	if dec != "smtp-password-123" {
		t.Fatalf("got %q", dec)
	}
}

func TestWrongSecret(t *testing.T) {
	enc, _ := Encrypt("a", "x")
	if _, err := Decrypt("b", enc); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Encrypt("", "x"); err == nil {
		t.Fatal("expected error on empty secret")
	}
}
