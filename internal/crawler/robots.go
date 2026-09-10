package crawler

import (
	"context"
	"sync"
	"time"

	"github.com/temoto/robotstxt"
)

// RobotsChecker caches robots.txt per host and answers allow/deny.
type RobotsChecker struct {
	client *limitedClient
	mu     sync.Mutex
	cache  map[string]*robotsEntry
	ttl    time.Duration
	agent  string
}

type robotsEntry struct {
	data *robotstxt.RobotsData
	at   time.Time
}

type limitedClient struct {
	do func(ctx context.Context, url string) (int, []byte, error)
}

// NewRobotsChecker builds a checker using the guarded client.
func NewRobotsChecker(g Guard, opt Options) *RobotsChecker {
	cl := g.NewClient(opt)
	if opt.UserAgent != "" {
		// keep agent short for robots matching
	}
	return &RobotsChecker{
		client: &limitedClient{do: func(ctx context.Context, u string) (int, []byte, error) {
			return fetchBytes(ctx, cl, u, opt.UserAgent, 1<<20)
		}},
		cache: map[string]*robotsEntry{},
		ttl:   24 * time.Hour,
		agent: "LeadForgeBot",
	}
}

// Allowed reports whether path may be fetched. Unknown hosts / fetch failures
// default to allow (fail-open for availability, rate limits still apply), but
// explicit Disallow is always honored.
func (r *RobotsChecker) Allowed(ctx context.Context, pageURL, host string) bool {
	r.mu.Lock()
	e, ok := r.cache[host]
	r.mu.Unlock()
	if !ok || time.Since(e.at) > r.ttl {
		data := r.fetch(ctx, host)
		r.mu.Lock()
		r.cache[host] = &robotsEntry{data: data, at: time.Now()}
		e = r.cache[host]
		r.mu.Unlock()
	}
	if e.data == nil {
		return true
	}
	path := pagePath(pageURL)
	grp := e.data.FindGroup(r.agent)
	return grp.Test(path)
}

func (r *RobotsChecker) fetch(ctx context.Context, host string) *robotstxt.RobotsData {
	for _, scheme := range []string{"https", "http"} {
		status, body, err := r.client.do(ctx, scheme+"://"+host+"/robots.txt")
		if err != nil || status != 200 {
			continue
		}
		if data, err := robotstxt.FromBytes(body); err == nil {
			return data
		}
	}
	return nil
}
