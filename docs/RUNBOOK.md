# Production runbook

## PostgreSQL down

`/health` may remain alive while `/ready` returns 503. Check connectivity,
credentials, pool saturation, and disk. Restore Postgres first; workers are
restarted by their supervisor. Never delete migration history.

## Redis down or queue growing

Check Redis memory, connectivity, and `/admin/queue`. `/ready` must be 503
while Redis is unavailable. Restart workers if pending jobs were lost. A Redis
semaphore outage falls back to a bounded local limiter and is degraded capacity.

## Worker or scheduler down

Inspect heartbeats at `/admin/health` and restart the missing systemd unit. The
watchdog marks searches without a recent heartbeat as failed; rerun only after
checking whether the original work committed.

## Google Places errors

`ZERO_RESULTS` is a normal empty result. `REQUEST_DENIED`, `INVALID_REQUEST`,
`OVER_QUERY_LIMIT`, and `UNKNOWN_ERROR` require configuration, request, quota,
or provider diagnosis. Never put the API key in a ticket or log.

## Crawler failure spike

Inspect `/admin/crawlers`, robots blocks, DNS/SSRF counts, 403/429/5xx errors,
and browser availability. Reduce workers before increasing timeouts. Preserve
robots compliance and polite public-domain validation defaults.

## SMTP bounce spike

Pause the campaign, inspect message failures and suppression entries, verify the
relay and sender domain, and resume only after a small test.

## Webhook failures

Check delivery records, target TLS/DNS, response codes, and signature
verification. Targets are HTTPS-only by default, private IPs are rejected, and
recipients should deduplicate with `X-LeadForge-Event-ID`.

## Emergency containment

Disable the affected integration or tenant, revoke exposed API keys, rotate the
relevant secret, preserve logs and audit rows, and record the incident. Never
print payloads containing authorization headers, passwords, or tokens.
