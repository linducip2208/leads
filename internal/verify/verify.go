// Package verify defines email verification. The built-in SyntaxVerifier needs
// no vendor; external providers can implement Verifier later.
package verify

import (
	"context"
	"net/mail"
	"strings"
)

// Statuses for email verification.
const (
	StatusUnknown    = "unknown"
	StatusValid      = "valid"
	StatusInvalid    = "invalid"
	StatusRisky      = "risky"
	StatusCatchAll   = "catch_all"
	StatusDisposable = "disposable"
)

// Result is a verification outcome.
type Result struct {
	Email  string
	Status string
	Reason string
}

// Verifier checks an email address.
type Verifier interface {
	Verify(ctx context.Context, email string) (Result, error)
}

// SyntaxVerifier performs format, role-account and disposable-domain checks.
type SyntaxVerifier struct{}

func (SyntaxVerifier) Verify(_ context.Context, email string) (Result, error) {
	e := strings.ToLower(strings.TrimSpace(email))
	if e == "" {
		return Result{Email: email, Status: StatusInvalid, Reason: "empty"}, nil
	}
	if _, err := mail.ParseAddress(e); err != nil {
		return Result{Email: e, Status: StatusInvalid, Reason: "bad format"}, nil
	}
	parts := strings.Split(e, "@")
	if len(parts) != 2 || !strings.Contains(parts[1], ".") {
		return Result{Email: e, Status: StatusInvalid, Reason: "bad domain"}, nil
	}
	domain := parts[1]
	if disposableDomains[domain] {
		return Result{Email: e, Status: StatusDisposable, Reason: "disposable domain"}, nil
	}
	local := parts[0]
	if roleAccounts[local] {
		return Result{Email: e, Status: StatusRisky, Reason: "role account"}, nil
	}
	return Result{Email: e, Status: StatusValid, Reason: "syntax ok"}, nil
}

var roleAccounts = map[string]bool{
	"info": true, "admin": true, "support": true, "sales": true, "hello": true,
	"contact": true, "help": true, "mail": true, "office": true, "cs": true,
	"marketing": true, "billing": true, "noreply": true, "no-reply": true,
}

var disposableDomains = map[string]bool{
	"mailinator.com": true, "tempmail.com": true, "10minutemail.com": true,
	"guerrillamail.com": true, "yopmail.com": true, "trashmail.com": true,
	"getnada.com": true, "temp-mail.org": true, "maildrop.cc": true,
}
