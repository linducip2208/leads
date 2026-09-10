# LeadForge

Lead discovery, enrichment and CRM in one Go workspace. Search companies by
keyword/industry/location, crawl public websites with a compliant bounded
crawler, deduplicate into companies & contacts, score with transparent rules,
and work everything through a CRM pipeline — **no AI required**.

Stack: **Go · PostgreSQL (pgx) · Redis + Asynq · templ + HTMX · Colly/goquery**

## Architecture

```
POST /finder/search
  → lead_searches row (status=queued)
  → Asynq job (search queue)
      ↓ worker (cmd/worker)
      discovery: Web Search · Public Directory (OSM) · Manual URLs · CSV · Custom API
      ↓ per candidate
      raw_leads → normalize → dedupe → crawl site (Colly, guarded) →
      companies + contacts → rule scoring → leads
      ↓ progress counters on lead_searches (SSE on /searches/{id})
```

Packages:

| Package | Responsibility |
|---|---|
| `internal/source` | `LeadSource` connectors (manual, CSV, web search, OSM directory, custom API, Google Places adapter dormant without key) |
| `internal/crawler` | SSRF guard, robots.txt, per-domain throttle, retry/backoff, goquery extractor, Colly site engine |
| `internal/lead` | Normalization (incl. ID phones `08…→+62…`) + dedupe with confidence |
| `internal/scoring` | Rule engine (built-in weights + tenant `scoring_rules`), 0–100, Low/Medium/Strong/Hot |
| `internal/enrichment` | Website enrichment provider (confidence + provenance) |
| `internal/verify` | Email verification interface + syntax/disposable/role-account checker |
| `internal/search` | Pipeline orchestrator (`Runner`: discover → process → score → counters) |
| `internal/queue` | Asynq task types, enqueue client, inspector + redis heartbeats |
| `internal/web` + `web/` | Routes, handlers, templ pages (finder, searches, leads+drawer, companies, people, imports, pipeline, deals, tasks, activities, scoring, ICP, segments, lists, enrichment, admin) |

## Requirements

- Go 1.24+
- PostgreSQL 14+ with `citext` extension (`CREATE EXTENSION citext;` is in migration 0001, needs a privileged user once)
- Redis 6+
- `templ` CLI (`go install github.com/a-h/templ/cmd/templ@latest`) for template changes

## PostgreSQL setup

```sql
CREATE DATABASE leadforge;
-- run once as superuser if the app user cannot create extensions:
-- CREATE EXTENSION IF NOT EXISTS citext;
```

```powershell
$env:DATABASE_URL = "postgres://postgres@127.0.0.1:5432/leadforge?sslmode=disable"
make migrate   # or: go run ./cmd/migrate up
```

Migrations live in `db/migrations/` (embedded, applied in order via
`schema_migrations`). Never edit an applied migration — add a new one.

## Redis setup

Any local Redis works. A portable Windows build is vendored under
`tools/redis/` (kept for developer convenience; for production use an
external/managed Redis instead):

```powershell
.\tools\redis\redis-server.exe tools\redis\redis.windows.conf
```

Default address `localhost:6379` (`REDIS_ADDR`).

## Environment

Copy `.env.example` to `.env` (or export vars). Minimum for local dev:
`DATABASE_URL`, `REDIS_ADDR`. `SESSION_SECRET` is required in production.

## Run

```powershell
make dev        # web  → http://localhost:8080
make worker     # background searches (separate terminal)
make scheduler  # periodic sweep + stall watchdog (separate terminal)
```

Open `/finder`, search (e.g. keyword `software house`, city `Jakarta`,
limit `100`), click **Find Leads** → redirected to `/searches/{id}` with a
live SSE progress bar. Results land in `/leads`, `/companies`, `/people`.

> Tip: paste known company URLs into **Seed URLs** to crawl directly without
> discovery. Discovery needs outbound internet (DuckDuckGo HTML / Nominatim).

## Lead Finder flow

1. `POST /finder/search` validates, stores `lead_searches` (`queued`) + filters JSON, enqueues `search:run`.
2. Worker claims (`queued→running`), streams candidates from selected sources (capped at `limit_count`), processes with 4-way bounded concurrency.
3. Per candidate: `raw_leads` insert → `normalize` → `dedupe` (domain 95 / phone 90 / email 90 / name+city 75 / name 55; auto-link ≥ 60) → crawl (bounded pages/depth, robots honored, SSRF-guarded) → company upsert → contact + email check → `lead_scores` + lead status (`≥90 hot`, `≥50 qualified`) → counters flushed.
4. Pause/resume/cancel are DB flags polled by the worker; processed data is kept on cancel. Stalled `running` jobs are failed by the watchdog (scheduler, every 5m).

## Crawler

- Engine: Colly (async, per-domain parallelism + delay) with a guarded
  transport (SSRF-safe dial, redirect revalidation, body cap); parsing via
  goquery (emails incl. `mailto:`, phones, `wa.me`, socials, tech fingerprints).
- Compliance: robots.txt honored (colly + pre-check), 750ms+ per-domain
  politeness delay, retries with backoff on 429/5xx, timeouts everywhere.
- Security: only `http/https`, no credentials in URL, blocks loopback, private
  RFC1918, link-local (incl. cloud metadata `169.254.169.254`), multicast;
  redirects re-resolved. `CRAWLER_ALLOW_PRIVATE=true` exists for local E2E
  fixtures only.

## Scoring

Built-in: website +10, email +15, verified +20, phone +10, WhatsApp +10,
industry match +20, location +10, employee +15 → clamped 0–100.
Tenant rules (`/scoring`) stack on top. Labels: 0–49 Low, 50–69 Medium,
70–89 Strong, 90–100 Hot.

## CRM

Default 10-stage pipeline is seeded per tenant on first `/pipeline` visit
(New → … → Won/Lost). Deals move by drag & drop (HTMX + local JS) or stage
buttons; tasks (call/email/WhatsApp/meeting/follow-up/general) and the
activity feed cover follow-ups. Segments are dynamic filters, lists are manual
(via the Leads bulk bar).

## Campaigns & outreach

Audience (segment/list) → template → sequence steps → sending account →
schedule → launch. The worker (`outreach:send`, ticked every minute by the
scheduler) sends due emails with enforced safety: suppression list,
invalid/disposable filtering, stop-on-reply, per-account hourly/daily limits.
Hard bounces (5xx) auto-suppress and invalidate the address; failures are
recorded per message, never fatal to the batch.

- Sending accounts live under `/settings/email-accounts` (SMTP, passwords
  AES-256-GCM encrypted, Test button dials the relay for real).
- Every email carries `List-Unsubscribe` + footer link (`/u/{token}`, HMAC
  signed, one-click).
- Replies arrive via `POST /webhooks/inbound?key=INBOUND_WEBHOOK_KEY`
  (`{"to","from","subject","body"}`) into `/inbox`, flipping contacts to
  replied. Manual replies send through the default account.

## Analytics

`/analytics/leads|campaigns|sales|sources`: KPIs, breakdown bars and 14-day
charts from live aggregates (lead funnel, reply rates, pipeline value, win
rate, channel quality).

## AI (optional, off by default)

`internal/ai` defines the `Provider` interface; `OpenAICompatible` covers
OpenAI/DeepSeek/GLM-compatible endpoints. Nothing in the pipeline calls it
unless `AI_ENABLED=true` and a provider is configured.

## Testing

```powershell
make test   # go test ./...
make vet
```

Unit: phone/domain normalization, dedupe helpers, scoring, HTML extractor,
CSV/Nominatim parsers, SSRF guard, email checks. Integration:
tenant-isolation (`internal/search`, transactional rollback, needs local PG —
set `TEST_DATABASE_URL` or it uses the default dev DB; skips if unreachable).

End-to-end: start server + worker with `CRAWLER_ALLOW_PRIVATE=true`, create a
search with local fixture Seed URLs, assert `raw_leads → companies → leads`
and progress counters move.

## Production deployment

- Set `APP_ENV=production`, strong `SESSION_SECRET`, managed Postgres + Redis.
- Run migrations before boot; run `server`, `worker` (≥1), `scheduler` as
  separate services behind a process supervisor.
- `/ready` returns 503 unless Postgres **and** Redis are reachable.
- Never enable `CRAWLER_ALLOW_PRIVATE` in production.
