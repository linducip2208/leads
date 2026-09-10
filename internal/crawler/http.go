package crawler

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// Options tunes the crawl HTTP client.
type Options struct {
	Timeout      time.Duration
	MaxBodyBytes int64
	UserAgent    string
}

// DefaultOptions returns sane defaults.
func DefaultOptions() Options {
	return Options{
		Timeout:      15 * time.Second,
		MaxBodyBytes: 5 << 20,
		UserAgent:    "LeadForgeBot/1.0 (+lead discovery; respects robots.txt)",
	}
}

// NewClient builds a guarded HTTP client: SSRF-safe dialing, redirect
// revalidation, timeouts.
func (g Guard) NewClient(opt Options) *http.Client {
	if opt.Timeout <= 0 {
		opt.Timeout = 15 * time.Second
	}
	if opt.MaxBodyBytes <= 0 {
		opt.MaxBodyBytes = 5 << 20
	}
	tr := &http.Transport{
		DialContext:           g.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: 2 * time.Second,
		MaxIdleConns:          50,
		IdleConnTimeout:       60 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport: tr,
		Timeout:   opt.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("crawler: too many redirects")
			}
			u, err := g.ValidateURL(req.URL.String())
			if err != nil {
				return err
			}
			if _, err := g.CheckHost(req.Context(), u.Host); err != nil {
				return err
			}
			return nil
		},
	}
}

// GuardedTransport wraps a base RoundTripper with a response body cap so even
// library-driven fetches (colly) cannot load unbounded bodies into memory.
func GuardedTransport(g Guard, maxBodyBytes int64) http.RoundTripper {
	base := &http.Transport{
		DialContext:           g.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          100,
		IdleConnTimeout:       60 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if maxBodyBytes <= 0 {
		maxBodyBytes = 5 << 20
	}
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		resp, err := base.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		resp.Body = &limitedReadCloser{R: io.LimitReader(resp.Body, maxBodyBytes+1), C: resp.Body, Max: maxBodyBytes}
		return resp, nil
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// limitedReadCloser errors once more than Max bytes are read.
type limitedReadCloser struct {
	R   io.Reader
	C   io.Closer
	Max int64
	n   int64
}

func (l *limitedReadCloser) Read(p []byte) (int, error) {
	n, err := l.R.Read(p)
	l.n += int64(n)
	if l.n > l.Max {
		return n, fmt.Errorf("crawler: response body exceeds %d bytes", l.Max)
	}
	return n, err
}

func (l *limitedReadCloser) Close() error { return l.C.Close() }

var _ = net.IPv4len
