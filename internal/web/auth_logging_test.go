package web

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestAuthMailLogsNeverContainBearerTokens(t *testing.T) {
	var logs bytes.Buffer
	s := &Server{Log: slog.New(slog.NewTextHandler(&logs, nil))}
	token := "reset-token-must-not-be-logged"

	s.mailVerifyLink("person@example.com", token)
	s.mailResetLink("person@example.com", token)

	if strings.Contains(logs.String(), token) {
		t.Fatalf("authentication bearer token leaked into logs: %s", logs.String())
	}
}
