# Security policy

## Supported versions

The `main` branch is the supported release line. Deployments should run the
Go version declared in `go.mod` and CI (`1.26.2`).

## Reporting a vulnerability

Do not disclose exploitable details in a public issue. Send a private report to
the repository maintainer with reproduction steps, affected route/package,
impact, and a safe remediation suggestion. Do not include real tenant data or
secrets.

## Secret handling

`SESSION_SECRET`, `APP_ENCRYPTION_KEY`, `INBOUND_WEBHOOK_KEY`, SMTP passwords,
provider keys, webhook secrets, and bearer tokens are never committed or
logged. API keys are stored as SHA-256 hashes and shown only once. Encrypted
credentials use AES-256-GCM with `APP_ENCRYPTION_KEY`; production requires a
dedicated 32+ byte key.

## Network and crawler policy

Only HTTP(S) is accepted. User-controlled URLs are parsed, DNS-resolved, and
validated against loopback, private, link-local, multicast, unspecified, and
cloud metadata addresses before dialing. Redirects are revalidated and limited
to five. Crawler jobs honor robots.txt, are bounded, and use polite defaults.
`CRAWLER_ALLOW_PRIVATE` and `ALLOW_INSECURE_WEBHOOKS` are local-development
exceptions and must be false in production.

## Security assumptions

Run behind TLS termination, keep Postgres and Redis on private networks, use a
least-privileged database role, rotate secrets through a controlled deployment,
and protect `/metrics` at the reverse proxy if it is not intended to be public.
