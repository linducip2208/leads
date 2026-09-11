# LeadForge architecture

```text
                         HTTPS
                           |
                        Nginx/Caddy
                           |
                    cmd/server (templ/HTMX)
                    /health /ready /metrics
                       |              |
                 PostgreSQL       Redis/Asynq
                       |              |
              cmd/worker <------- queue jobs
                 |      \
             search     outreach/webhook
                 |
       source connectors -> crawler/Colly -> guarded HTTP
                                  |
                              chromedp pool

                    cmd/scheduler -> periodic Asynq jobs
```

PostgreSQL is the canonical business store. Redis holds queue state, bounded
distributed crawler permits, and liveness heartbeats. The server handles
requests and never performs unbounded crawling inline. Workers claim searches,
stream bounded source results, normalize/dedupe candidates, crawl public sites,
and persist tenant-scoped CRM data. The scheduler only enqueues maintenance and
outreach work.

Every route that reads or mutates tenant data carries the authenticated tenant
ID into SQL predicates. API keys are tenant-scoped and keyset pagination is
used for large lead listings. Outbound custom API and webhook requests reuse
the crawler network guard so DNS rebinding and SSRF checks are centralized.
