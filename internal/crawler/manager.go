package crawler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"leadforge/internal/platform/config"
	"leadforge/internal/redisx"
)

// Manager enforces global crawl concurrency across searches, per-domain
// politeness, cooldowns, metrics and browser fallback.
type Manager struct {
	Guard   Guard
	Robots  *RobotsChecker
	Domains *DomainLimiter
	Browser *BrowserPool

	global chan struct{}

	// dist is the cross-worker semaphore (nil = single process mode).
	dist   *redisx.Semaphore
	distOn bool

	coolMu    sync.Mutex
	cooldowns map[string]time.Time
	failures  map[string]int

	pool *pgxpool.Pool // optional: persists domain cooldowns

	pagesOK   atomic.Int64
	pagesFail atomic.Int64
	robotsHit atomic.Int64
	ssrfHit   atomic.Int64
	browserOK atomic.Int64
	nanos     atomic.Int64
}

// NewManager builds a manager from app config.
func NewManager(cfg *config.Config, pool *pgxpool.Pool) *Manager {
	g := Guard{AllowPrivate: cfg.CrawlerAllowPrivate}
	m := &Manager{
		Guard:     g,
		Robots:    NewRobotsChecker(g, Options{Timeout: cfg.CrawlerTimeout, MaxBodyBytes: 1 << 20, UserAgent: "LeadForgeBot/1.0"}),
		Domains:   NewDomainLimiter(cfg.CrawlerDomainConcurrency, 750*time.Millisecond),
		global:    make(chan struct{}, cfg.CrawlerGlobalWorkers),
		cooldowns: map[string]time.Time{},
		failures:  map[string]int{},
		pool:      pool,
	}
	if m.globalCap() <= 0 {
		m.global = make(chan struct{}, 40)
	}
	// cross-worker global cap via Redis (self-healing TTL permits).
	// Falls back to the local channel if Redis is unreachable.
	if cfg.RedisAddr != "" && cfg.CrawlerGlobalWorkers > 0 {
		rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
		m.dist = redisx.NewSemaphore(rdb, "leadforge:crawl:permits", cfg.CrawlerGlobalWorkers, 60*time.Second)
		m.distOn = true
	}
	m.Browser = NewBrowserPool(BrowserOptions{
		Enabled: cfg.BrowserEnabled, Workers: cfg.BrowserWorkers,
		Timeout: cfg.BrowserTimeout, MaxPages: cfg.BrowserMaxPages,
	})
	return m
}

func (m *Manager) globalCap() int { return cap(m.global) }

// acquire takes a global slot: Redis semaphore across workers when
// available, local channel otherwise (fail-open keeps crawls flowing).
func (m *Manager) acquire(ctx context.Context) (release func(), ok bool) {
	if m.distOn && m.dist != nil {
		var b [8]byte
		_, _ = rand.Read(b[:])
		tok := hex.EncodeToString(b[:])
		slot := m.dist.Acquire(ctx, tok)
		if slot >= 0 {
			return func() { m.dist.Release(context.Background(), slot, tok) }, true
		}
		// redis denied/unreachable: fall through to local limiter
	}
	select {
	case <-ctx.Done():
		return nil, false
	case m.global <- struct{}{}:
		return func() { <-m.global }, true
	}
}

// Metrics is a point-in-time snapshot for admin display.
type Metrics struct {
	PagesOK   int64
	PagesFail int64
	RobotsHit int64
	SSRFHit   int64
	BrowserOK int64
	AvgMs     int64
	GlobalCap int
	GlobalUse int
}

// Snapshot returns current metrics.
func (m *Manager) Snapshot() Metrics {
	ok := m.pagesOK.Load()
	total := ok + m.pagesFail.Load()
	var avg int64
	if total > 0 {
		avg = m.nanos.Load() / total / 1e6
	}
	return Metrics{
		PagesOK: ok, PagesFail: m.pagesFail.Load(), RobotsHit: m.robotsHit.Load(),
		SSRFHit: m.ssrfHit.Load(), BrowserOK: m.browserOK.Load(), AvgMs: avg,
		GlobalCap: cap(m.global), GlobalUse: len(m.global),
	}
}

// Crawl fetches one site with global fairness: at most GlobalWorkers crawls
// run at once no matter how many searches compete. Cooled-down domains are
// skipped fast. Low-quality JS shells trigger the browser fallback.
func (m *Manager) Crawl(ctx context.Context, startURL string, cfg SiteConfig) *SiteResult {
	t0 := time.Now()
	release, ok := m.acquire(ctx)
	if !ok {
		return &SiteResult{Data: &Extracted{Socials: map[string]string{}}, Errors: []string{"crawl slots exhausted"}}
	}
	defer release()

	u, err := m.Guard.ValidateURL(startURL)
	if err != nil {
		m.ssrfHit.Add(1)
		return &SiteResult{Data: &Extracted{Socials: map[string]string{}}, Errors: []string{err.Error()}}
	}
	if cd := m.cooled(u.Host); cd {
		return &SiteResult{Data: &Extracted{Socials: map[string]string{}}, Errors: []string{"domain cooling down"}}
	}
	if !m.Robots.Allowed(ctx, u.String(), u.Host) {
		m.robotsHit.Add(1)
		m.noteFailure(u.Host, "robots")
		return &SiteResult{Data: &Extracted{Socials: map[string]string{}}, Errors: []string{"blocked by robots.txt"}}
	}

	res := CrawlSite(ctx, m.Guard, m.Robots, startURL, cfg)
	elapsed := time.Since(t0)
	m.nanos.Add(int64(elapsed))
	if res.Pages > 0 {
		m.pagesOK.Add(int64(res.Pages))
	} else {
		m.pagesFail.Add(1)
		m.noteFailure(u.Host, "nofetch")
	}

	// browser fallback: JS shell or thin content
	if m.Browser.Available() && res.Pages > 0 && needsBrowser(res.Data, res.Pages) {
		if bres := m.Browser.CrawlSite(ctx, m, startURL, cfg.MaxPages); bres != nil {
			res.Data.Merge(bres.Data)
			res.Pages += bres.Pages
			m.browserOK.Add(1)
		}
	}
	return res
}

// Cooldown represents an active domain cooldown.
type Cooldown struct {
	Domain string
	Until  time.Time
}

// Cooldowns lists active in-memory cooldowns.
func (m *Manager) Cooldowns() []Cooldown {
	m.coolMu.Lock()
	defer m.coolMu.Unlock()
	var out []Cooldown
	now := time.Now()
	for d, u := range m.cooldowns {
		if now.Before(u) {
			out = append(out, Cooldown{Domain: d, Until: u})
		}
	}
	return out
}
func (m *Manager) cooled(host string) bool {
	h := stripPortLower(host)
	m.coolMu.Lock()
	until, ok := m.cooldowns[h]
	m.coolMu.Unlock()
	if ok && time.Now().Before(until) {
		return true
	}
	if m.pool != nil {
		var u time.Time
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := m.pool.QueryRow(ctx, `SELECT until FROM domain_cooldowns WHERE domain=$1`, h).Scan(&u); err == nil && time.Now().Before(u) {
			return true
		}
	}
	return false
}

// noteFailure tracks consecutive failures; 3 strikes in a row cools 10 min.
func (m *Manager) noteFailure(host, reason string) {
	h := stripPortLower(host)
	m.coolMu.Lock()
	m.failures[h]++
	n := m.failures[h]
	m.coolMu.Unlock()
	if n < 3 {
		return
	}
	until := time.Now().Add(10 * time.Minute)
	m.coolMu.Lock()
	m.cooldowns[h] = until
	m.failures[h] = 0
	m.coolMu.Unlock()
	if m.pool != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = m.pool.Exec(ctx, `
			INSERT INTO domain_cooldowns (domain, reason, until, failures)
			VALUES ($1,$2,$3,1)
			ON CONFLICT (domain) DO UPDATE SET reason=$2, until=$3, failures=domain_cooldowns.failures+1`,
			h, reason, until)
	}
}

// noteSuccess resets the failure streak.
func (m *Manager) noteSuccess(host string) {
	h := stripPortLower(host)
	m.coolMu.Lock()
	delete(m.failures, h)
	m.coolMu.Unlock()
}

func stripPortLower(host string) string {
	h := stripPort(host)
	l := len(h)
	if l > 0 && h[l-1] == '.' {
		h = h[:l-1]
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		if c >= 'A' && c <= 'Z' {
			b := []byte(h)
			for j := range b {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return h
}
