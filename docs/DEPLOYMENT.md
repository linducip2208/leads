# Production deployment

## Host prerequisites

Use Ubuntu 24.04 or equivalent with 2–4 vCPU for a small deployment. Run
PostgreSQL 14+ with `citext` and Redis 6+ on private interfaces. Chrome is
optional: without it the crawler remains HTTP-only.

## Build and configure

```sh
git clone https://github.com/linducip2208/leads.git
cd leads
go mod download
go build -trimpath -o bin/server ./cmd/server
go build -trimpath -o bin/worker ./cmd/worker
go build -trimpath -o bin/scheduler ./cmd/scheduler
go build -trimpath -o bin/migrate ./cmd/migrate
```

Copy `.env.example` to a protected environment file. Set production
`DATABASE_URL`, `REDIS_ADDR`, `APP_URL`, random 32+ byte `SESSION_SECRET`,
`APP_ENCRYPTION_KEY`, and `INBOUND_WEBHOOK_KEY`. Keep
`CRAWLER_ALLOW_PRIVATE=false`, `ALLOW_INSECURE_WEBHOOKS=false`, and
`AI_ENABLED=false` unless explicitly required.

For short-lived diagnostics, set `PPROF_ENABLED=true`. Profiles are available
only to authenticated users with the platform-admin permission under
`/admin/pprof/`; keep the reverse proxy from exposing that path publicly and
turn the setting off after collecting the profile.

## Migrate and start

```sh
./bin/migrate up
./bin/doctor
```

Start `server`, `worker`, and `scheduler` as separate systemd services. Do not
run migrations from worker startup. Example units are in `deploy/`.

## Reverse proxy and TLS

Use `deploy/nginx.conf` as a starting point. Proxy the SSE route with buffering
disabled, preserve `X-Request-ID`, and terminate HTTPS at the proxy. Enable
HSTS only when every public hostname is HTTPS.

## Docker Compose

```sh
docker compose up --build
```

Compose starts PostgreSQL, Redis, a one-shot migration container, and the three
application processes. It is suitable for development/staging; use managed
services and secret injection for production.

## Backup and restore

PostgreSQL is canonical and must be backed up daily:

```sh
pg_dump --format=custom "$DATABASE_URL" > leadforge-$(date +%F).dump
pg_restore --clean --if-exists --dbname="$DATABASE_URL" leadforge-YYYY-MM-DD.dump
```

Test restores on a separate database and retain backups according to policy.
Redis is disposable queue/coordination state; Redis loss can drop pending jobs,
so reconcile affected searches after recovery.

## Release gate

Run `make release-check`, apply migrations against a fresh database, run
`go test -race ./...`, then execute a fixture search before promotion.
