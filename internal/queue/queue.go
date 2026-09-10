// Package queue wires Redis + Asynq: task types, enqueue client, server and
// inspector helpers. Queues: critical, search, crawler, enrichment,
// verification, outreach, ai, export, maintenance.
package queue

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// Task types.
const (
	TypeSearchRun       = "search:run"
	TypeSweep           = "maintenance:sweep"
	TypeWatchdog        = "maintenance:watchdog"
	TypeOutreachTick    = "outreach:tick"
	TypeOutreachSend    = "outreach:send"
	TypeLeadRefresh     = "lead:refresh"
	TypeLeadBulkRefresh = "lead:bulk-refresh"
	TypeRefreshStale    = "maintenance:refresh-stale"
)

// Queue names.
const (
	QCritical    = "critical"
	QSearch      = "search"
	QCrawler     = "crawler"
	QEnrichment  = "enrichment"
	QVerify      = "verification"
	QOutreach    = "outreach"
	QAI          = "ai"
	QExport      = "export"
	QMaintenance = "maintenance"
)

// AllQueues lists queues in priority order.
var AllQueues = []string{QCritical, QSearch, QCrawler, QEnrichment, QVerify, QOutreach, QAI, QExport, QMaintenance}

// SearchPayload runs one lead search.
type SearchPayload struct {
	SearchID string `json:"search_id"`
}

// OutreachPayload sends due emails for one campaign.
type OutreachPayload struct {
	CampaignID string `json:"campaign_id"`
}

// RefreshPayload refreshes one lead.
type RefreshPayload struct {
	LeadID string `json:"lead_id"`
}

// BulkRefreshPayload refreshes up to 100 leads.
type BulkRefreshPayload struct {
	LeadIDs []string `json:"lead_ids"`
}

// RedisOpt builds asynq redis options from an address.
func RedisOpt(addr string) asynq.RedisClientOpt {
	return asynq.RedisClientOpt{Addr: addr}
}

// Client enqueues tasks.
type Client struct {
	c *asynq.Client
}

// NewClient builds an enqueue client.
func NewClient(addr string) *Client {
	return &Client{c: asynq.NewClient(RedisOpt(addr))}
}

// Close closes the client.
func (cl *Client) Close() error { return cl.c.Close() }

// EnqueueSearch queues a search run on the search queue.
func (cl *Client) EnqueueSearch(ctx context.Context, searchID string) error {
	body, _ := json.Marshal(SearchPayload{SearchID: searchID})
	_, err := cl.c.EnqueueContext(ctx, asynq.NewTask(TypeSearchRun, body),
		asynq.Queue(QSearch), asynq.MaxRetry(2), asynq.Timeout(6*time.Hour))
	return err
}

// EnqueueSweep queues session/token cleanup.
func (cl *Client) EnqueueSweep(ctx context.Context) error {
	_, err := cl.c.EnqueueContext(ctx, asynq.NewTask(TypeSweep, nil),
		asynq.Queue(QMaintenance), asynq.MaxRetry(1))
	return err
}

// EnqueueWatchdog queues the stall watchdog.
func (cl *Client) EnqueueWatchdog(ctx context.Context) error {
	_, err := cl.c.EnqueueContext(ctx, asynq.NewTask(TypeWatchdog, nil),
		asynq.Queue(QMaintenance), asynq.MaxRetry(1), asynq.Unique(time.Hour))
	return err
}

// EnqueueOutreachTick scans for due campaigns every minute.
func (cl *Client) EnqueueOutreachTick(ctx context.Context) error {
	_, err := cl.c.EnqueueContext(ctx, asynq.NewTask(TypeOutreachTick, nil),
		asynq.Queue(QOutreach), asynq.MaxRetry(1), asynq.Unique(2*time.Minute))
	return err
}

// EnqueueOutreachSend queues one campaign send batch (deduped per campaign).
func (cl *Client) EnqueueOutreachSend(ctx context.Context, campaignID string) error {
	body, _ := json.Marshal(OutreachPayload{CampaignID: campaignID})
	_, err := cl.c.EnqueueContext(ctx, asynq.NewTask(TypeOutreachSend, body),
		asynq.Queue(QOutreach), asynq.MaxRetry(2), asynq.Timeout(30*time.Minute),
		asynq.Unique(5*time.Minute))
	return err
}

// EnqueueLeadRefresh queues a single lead refresh.
func (cl *Client) EnqueueLeadRefresh(ctx context.Context, leadID string) error {
	body, _ := json.Marshal(RefreshPayload{LeadID: leadID})
	_, err := cl.c.EnqueueContext(ctx, asynq.NewTask(TypeLeadRefresh, body),
		asynq.Queue(QEnrichment), asynq.MaxRetry(2), asynq.Timeout(10*time.Minute))
	return err
}

// EnqueueLeadBulkRefresh queues a bounded bulk refresh.
func (cl *Client) EnqueueLeadBulkRefresh(ctx context.Context, ids []string) error {
	if len(ids) > 100 {
		ids = ids[:100]
	}
	body, _ := json.Marshal(BulkRefreshPayload{LeadIDs: ids})
	_, err := cl.c.EnqueueContext(ctx, asynq.NewTask(TypeLeadBulkRefresh, body),
		asynq.Queue(QEnrichment), asynq.MaxRetry(1), asynq.Timeout(2*time.Hour))
	return err
}

// EnqueueRefreshStale queues the stale-hot-lead sweep.
func (cl *Client) EnqueueRefreshStale(ctx context.Context) error {
	_, err := cl.c.EnqueueContext(ctx, asynq.NewTask(TypeRefreshStale, nil),
		asynq.Queue(QMaintenance), asynq.MaxRetry(1), asynq.Unique(20*time.Hour))
	return err
}

// QueueStat is inspector output per queue.
type QueueStat struct {
	Queue     string
	Pending   int
	Active    int
	Scheduled int
	Retry     int
	Archived  int
	Processed int
	Failed    int
}

// Inspect returns per-queue stats for the admin queue page.
func Inspect(addr string) ([]QueueStat, error) {
	insp := asynq.NewInspector(RedisOpt(addr))
	defer insp.Close()
	var out []QueueStat
	for _, q := range AllQueues {
		info, err := insp.GetQueueInfo(q)
		if err != nil {
			// queue may not exist yet; report zeros
			out = append(out, QueueStat{Queue: q})
			continue
		}
		out = append(out, QueueStat{
			Queue: q, Pending: info.Pending, Active: info.Active,
			Scheduled: info.Scheduled, Retry: info.Retry, Archived: info.Archived,
			Processed: info.ProcessedTotal, Failed: info.FailedTotal,
		})
	}
	return out, nil
}

// PingRedis checks redis reachability (health checks).
func PingRedis(ctx context.Context, addr string) error {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return rdb.Ping(ctx).Err()
}

// Beat writes a liveness key with TTL (worker/scheduler heartbeats).
func Beat(ctx context.Context, addr, key string, ttl time.Duration) error {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	return rdb.Set(ctx, key, time.Now().UTC().Format(time.RFC3339), ttl).Err()
}

// LastBeat reads a liveness key; ok=false when missing/expired.
func LastBeat(ctx context.Context, addr, key string) (t time.Time, ok bool) {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	s, err := rdb.Get(ctx, key).Result()
	if err != nil {
		return time.Time{}, false
	}
	t, err = time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
