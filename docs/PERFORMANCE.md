# Performance notes

## Fresh local baseline

These numbers were rerun during the production-hardening pass on 2026-09-11,
not copied from an older report:

| Scenario | Duration | Saved | Throughput | Peak goroutines | Peak memory |
|---|---:|---:|---:|---:|---:|
| 100 mock candidates | 307 ms | 100 | 326.1/s | 8 | 3.0 MB |
| 100 fixture HTTP seeds | 2.193 s | 1 (99 same-domain duplicates) | 45.6/s | 49 | 2.9 MB |
| 1,000 mock candidates | 3.810 s | 1,000 | 262.5/s | 11 | 4.6 MB |
| 10,000 mock candidates | 2m24.45s | 10,000 | 69.2/s | 15 | 7.6 MB |

Environment: Windows x86_64, Go 1.26.2, PostgreSQL 18.3, Redis 7.2.5,
22 logical processors, 63.7 GB RAM, local database, mock source, current
working tree. These are local measurements, not a production guarantee; the
The 100-candidate measurements were rerun after delivery and tenant-validation
hardening. The 1k/10k runs are fresh local measurements from the same pass.
The 10k run is database-bound and should be repeated on the target VPS.

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

## EXPLAIN ANALYZE validation

On 2026-09-11, a 1,000-company fixture tenant was loaded and the important
plans were checked after `ANALYZE`: lead keyset used
`idx_leads_tenant_created_keyset` (101 rows, 0.240 ms), company keyset used
`idx_companies_tenant_created_keyset` (51 rows, 0.064 ms), raw-lead status
lookup used `idx_raw_leads_tenant_search_status` (0.050 ms), and canonical
domain dedupe used `uq_companies_tenant_domain` (0.058 ms). Statistics should
be refreshed by PostgreSQL autovacuum after large imports.
