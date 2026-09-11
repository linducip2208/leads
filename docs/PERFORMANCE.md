# Performance notes

## Fresh local baseline

These numbers were rerun during the production-hardening pass on 2026-09-11,
not copied from an older report:

| Scenario | Duration | Saved | Throughput | Peak goroutines | Peak memory |
|---|---:|---:|---:|---:|---:|
| 100 mock candidates | 342 ms | 100 | 292.1/s | 12 | 3.6 MB |
| 1,000 mock candidates | 2.401 s | 1,000 | 416.6/s | 15 | 5.0 MB |
| 10,000 mock candidates | 2m24.45s | 10,000 | 69.2/s | 15 | 7.6 MB |

Environment: Windows x86_64, Go 1.26.2, PostgreSQL 18.3, Redis 7.2.5,
22 logical processors, 63.7 GB RAM, local database, mock source, current
working tree. These are local measurements, not a production guarantee; the
10k run is database-bound and should be repeated on the target VPS.

Public crawler smoke test (four safe documentation domains, five workers,
one-second delay) completed in 45.366s: 4 success, 0 failed, 3 email-found,
3 phone-found, average quality 68, no robots/403/404/429/5xx failures.

## Starting presets

Tune from measurements, not promises:

| Profile | Search workers | Crawler global | Browser workers | DB max connections |
|---|---:|---:|---:|---:|
| Small VPS (2–4 vCPU) | 4 | 10 | 1 | 10 |
| Medium (4–8 vCPU) | 8 | 30 | 2 | 20 |
| Large (8–16 vCPU) | 16 | 60 | 4 | 40 |

Keep public-domain validation at five workers, one domain stream, and at least
one second delay. Increase only after checking provider policy, CPU, memory,
database pool wait, queue depth, and crawler error rates.

## Query audit commands

Use a staging/fresh copy and inspect plans before changing indexes:

```sql
EXPLAIN (ANALYZE, BUFFERS) SELECT ... FROM leads
  WHERE tenant_id = $1 ORDER BY created_at DESC, id DESC LIMIT 101;
EXPLAIN (ANALYZE, BUFFERS) SELECT ... FROM companies
  WHERE tenant_id = $1 AND domain = $2;
EXPLAIN (ANALYZE, BUFFERS) SELECT ... FROM raw_leads
  WHERE tenant_id = $1 AND search_id = $2 AND status = $3;
```

Large API and UI lists use cursor/keyset pagination. Fuzzy dedupe is bounded to
candidate matches and uses `pg_trgm` when available; the fallback uses bounded
prefix/LIKE candidates instead of scanning all tenants.
