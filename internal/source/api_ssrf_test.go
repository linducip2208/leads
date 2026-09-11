package source

import (
	"context"
	"testing"
)

func TestCustomAPISourceRejectsPrivateAndCredentialURLs(t *testing.T) {
	s := NewCustomAPISource()
	for _, endpoint := range []string{
		"http://localhost:8080/data",
		"http://127.0.0.1/data",
		"http://10.0.0.1/data",
		"http://172.16.0.1/data",
		"http://192.168.1.1/data",
		"http://[::1]/data",
		"http://user:password@example.com/data",
		"http://169.254.169.254/latest/meta-data",
	} {
		if _, err := s.Search(context.Background(), SearchQuery{APIURL: endpoint}); err == nil {
			t.Fatalf("expected custom API endpoint to be rejected: %s", endpoint)
		}
	}
}
