package outreach

import "testing"

func TestUnsubTokenRoundtrip(t *testing.T) {
	tok := UnsubToken("s3cret", "tenant-1", "contact-9")
	cid, ok := VerifyUnsubToken("s3cret", "tenant-1", tok)
	if !ok || cid != "contact-9" {
		t.Fatalf("roundtrip failed: %q %v", cid, ok)
	}
	if _, ok := VerifyUnsubToken("wrong", "tenant-1", tok); ok {
		t.Fatal("wrong secret accepted")
	}
	if _, ok := VerifyUnsubToken("s3cret", "tenant-2", tok); ok {
		t.Fatal("wrong tenant accepted")
	}
	if _, ok := VerifyUnsubToken("s3cret", "tenant-1", "garbage"); ok {
		t.Fatal("garbage accepted")
	}
}

func TestHardBounceClassify(t *testing.T) {
	if !isHardBounce(errString("550 5.1.1 mailbox unavailable")) {
		t.Fatal("550 should be hard bounce")
	}
	if isHardBounce(errString("connection refused")) {
		t.Fatal("refused should be soft")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
