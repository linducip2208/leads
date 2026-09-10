package verify

import (
	"context"
	"testing"
)

func TestSyntaxVerifier(t *testing.T) {
	v := SyntaxVerifier{}
	cases := map[string]string{
		"budi@perusahaan.co.id": StatusValid,
		"info@perusahaan.co.id": StatusRisky,
		"x@mailinator.com":      StatusDisposable,
		"not-an-email":          StatusInvalid,
		"":                      StatusInvalid,
	}
	for in, want := range cases {
		res, err := v.Verify(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != want {
			t.Errorf("Verify(%q) = %q, want %q", in, res.Status, want)
		}
	}
}
