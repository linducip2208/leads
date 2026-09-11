// Package crawler fetches public company websites with safety guardrails:
// SSRF protection, robots.txt compliance, bounded concurrency, retries.
package crawler

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Guard enforces crawl safety policy.
type Guard struct {
	// AllowPrivate permits loopback/private/link-local IPs. Dev/test only
	// (E2E fixtures). Never enable in production.
	AllowPrivate bool
}

// Blocked errors.
var (
	ErrBadScheme = fmt.Errorf("crawler: only http/https URLs are allowed")
	ErrNoHost    = fmt.Errorf("crawler: URL has no host")
	ErrBlockedIP = fmt.Errorf("crawler: target IP is blocked by crawl policy")
)

// ValidateURL parses and enforces scheme policy.
func (g Guard) ValidateURL(rawurl string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawurl))
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return nil, ErrNoHost
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrBadScheme
	}
	if user := u.User; user != nil {
		return nil, fmt.Errorf("crawler: URLs with credentials are not allowed")
	}
	return u, nil
}

// ValidateAndCheckURL validates URL syntax and current DNS answers. The
// guarded transport pins later dials to validated IPs to prevent DNS rebinding.
func (g Guard) ValidateAndCheckURL(ctx context.Context, rawurl string) (*url.URL, error) {
	u, err := g.ValidateURL(rawurl)
	if err != nil {
		return nil, err
	}
	if _, err := g.CheckHost(ctx, u.Hostname()); err != nil {
		return nil, err
	}
	return u, nil
}

// CheckHost resolves host and ensures every resolved IP is allowed.
func (g Guard) CheckHost(ctx context.Context, host string) ([]net.IP, error) {
	h := strings.TrimSuffix(strings.TrimSpace(stripPort(host)), ".")
	h = strings.Trim(h, "[]")
	if h == "" {
		return nil, ErrNoHost
	}
	if ip := net.ParseIP(h); ip != nil {
		if err := g.CheckIP(ip); err != nil {
			return nil, err
		}
		return []net.IP{ip}, nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", h)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("crawler: cannot resolve host: %w", err)
	}
	for _, ip := range ips {
		if err := g.CheckIP(ip); err != nil {
			return nil, err
		}
	}
	return ips, nil
}

// CheckIP blocks loopback, private, link-local, multicast and unspecified
// addresses (incl. cloud metadata 169.254.169.254 via link-local range).
func (g Guard) CheckIP(ip net.IP) error {
	// Explicitly block the cloud metadata address and its IPv4-mapped IPv6 form.
	if ip.Equal(net.ParseIP("169.254.169.254")) || ip.Equal(net.ParseIP("::ffff:169.254.169.254")) {
		return fmt.Errorf("%w: cloud metadata address", ErrBlockedIP)
	}
	if g.AllowPrivate {
		if ip.IsUnspecified() || ip.IsMulticast() {
			return fmt.Errorf("%w: %s", ErrBlockedIP, ip.String())
		}
		return nil
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("%w: %s", ErrBlockedIP, ip.String())
	}
	return nil
}

// DialContext resolves the host, validates IPs, then dials a validated IP
// directly (TOCTOU-safe: no second resolution inside the dialer). Each
// resolved IP is tried in turn.
func (g Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := g.CheckHost(ctx, host)
	if err != nil {
		return nil, err
	}
	var dialer net.Dialer
	// SNI/Host header still use the original hostname; only TCP target is pinned.
	var firstErr error
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

func stripPort(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}
