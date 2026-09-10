package crawler

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DomainLimiter enforces per-host concurrency and minimum interval between
// requests (politeness throttle).
type DomainLimiter struct {
	mu       sync.Mutex
	sem      map[string]chan struct{}
	last     map[string]time.Time
	maxConc  int
	interval time.Duration
}

// NewDomainLimiter builds a limiter.
func NewDomainLimiter(maxConcurrency int, minInterval time.Duration) *DomainLimiter {
	if maxConcurrency <= 0 {
		maxConcurrency = 2
	}
	if minInterval <= 0 {
		minInterval = 750 * time.Millisecond
	}
	return &DomainLimiter{
		sem:      map[string]chan struct{}{},
		last:     map[string]time.Time{},
		maxConc:  maxConcurrency,
		interval: minInterval,
	}
}

// Acquire blocks until a slot for host is available or ctx ends.
func (l *DomainLimiter) Acquire(ctx context.Context, host string) (release func(), err error) {
	l.mu.Lock()
	s, ok := l.sem[host]
	if !ok {
		s = make(chan struct{}, l.maxConc)
		l.sem[host] = s
	}
	wait := time.Until(l.last[host].Add(l.interval))
	l.mu.Unlock()
	if wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-t.C:
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case s <- struct{}{}:
	}
	return func() {
		l.mu.Lock()
		l.last[host] = time.Now()
		l.mu.Unlock()
		<-s
	}, nil
}

// DoWithRetry runs fn with exponential backoff on transient failures.
func DoWithRetry(ctx context.Context, attempts int, baseDelay time.Duration, fn func() error) error {
	if attempts <= 0 {
		attempts = 3
	}
	if baseDelay <= 0 {
		baseDelay = 500 * time.Millisecond
	}
	var err error
	for i := 0; i < attempts; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = fn(); err == nil {
			return nil
		}
		if !retryable(err) {
			return err
		}
		backoff := baseDelay << i
		backoff += time.Duration(rand.Int63n(int64(baseDelay)))
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return err
}

type statusError struct{ code int }

func (e *statusError) Error() string { return fmt.Sprintf("http status %d", e.code) }

// HTTPStatusError marks a fetch failure with its status code.
func HTTPStatusError(code int) error { return &statusError{code: code} }

func retryable(err error) bool {
	var se *statusError
	if e, ok := err.(*statusError); ok {
		_ = e
		_ = se
		code := e.code
		return code == 429 || code >= 500
	}
	// network/timeout errors are retryable; policy denials are not
	msg := err.Error()
	if strings.Contains(msg, "blocked by crawl policy") ||
		strings.Contains(msg, "only http/https") ||
		strings.Contains(msg, "too many redirects") ||
		strings.Contains(msg, "exceeds") {
		return false
	}
	return true
}

// fetchBytes GETs url with UA header and a body cap.
func fetchBytes(ctx context.Context, client *http.Client, rawurl, ua string, maxBytes int64) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return 0, nil, err
	}
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.1")
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if int64(len(body)) > maxBytes {
		return resp.StatusCode, nil, fmt.Errorf("crawler: response body exceeds %d bytes", maxBytes)
	}
	return resp.StatusCode, body, nil
}

func pagePath(rawurl string) string {
	u, err := url.Parse(rawurl)
	if err != nil || u.Path == "" {
		return "/"
	}
	if u.RawQuery != "" {
		return u.Path + "?" + u.RawQuery
	}
	return u.Path
}
