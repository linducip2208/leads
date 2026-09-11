# LeadForge dev commands. Run from the repository root.
# Windows (PowerShell): use the same targets, e.g. `make dev`.

GO ?= go
CSS_SRC = web/assets/css/app.css
CSS_OUT = static/css/app.css
# Standalone Tailwind v4 binary (dev-machine local, gitignored).
# Download from https://github.com/tailwindlabs/tailwindcss/releases
# and place it at tools/bin/tailwindcss.exe (Windows) or tools/bin/tailwindcss.
TAILWIND ?= tools/bin/tailwindcss.exe

.PHONY: setup dev build test race vet migrate migrate-down worker scheduler templ css seed bench crawler-test doctor release-check

setup: ## Install deps, generate templates, migrate DB
	$(GO) mod tidy
	$(GO) run github.com/a-h/templ/cmd/templ generate
	$(GO) run ./cmd/migrate up

dev: ## Run web server (needs Postgres + Redis)
	$(GO) run ./cmd/server

worker: ## Run background worker (needs Postgres + Redis)
	$(GO) run ./cmd/worker

scheduler: ## Run periodic scheduler (needs Redis)
	$(GO) run ./cmd/scheduler

build: ## Build all binaries into bin/
	@mkdir -p bin
	$(GO) build -o bin/server ./cmd/server
	$(GO) build -o bin/worker ./cmd/worker
	$(GO) build -o bin/scheduler ./cmd/scheduler
	$(GO) build -o bin/migrate ./cmd/migrate
	$(GO) build -o bin/benchmark-search ./cmd/benchmark-search

test: ## Run all tests
	$(GO) test ./...

race: ## Run all tests with the Go race detector
	$(GO) test -race ./...

bench: ## Scale simulation (COUNT=1000 MODE=mock)
	$(GO) run ./cmd/benchmark-search --count=$(or $(COUNT),1000) --mode=$(or $(MODE),mock)

vet: ## go vet + gofmt check
	$(GO) vet ./...
	gofmt -l internal web cmd db

migrate: ## Apply pending migrations + seed system roles
	$(GO) run ./cmd/migrate up

migrate-down: ## Revert last migration
	$(GO) run ./cmd/migrate down

templ: ## Regenerate templ files
	$(GO) run github.com/a-h/templ/cmd/templ generate

css: ## Rebuild static/css/app.css from web/assets (needs Tailwind standalone CLI)
	$(TAILWIND) -i $(CSS_SRC) -o $(CSS_OUT) --minify

seed: ## Re-apply migrations (idempotent seeds included)
	$(GO) run ./cmd/migrate up

crawler-test: ## Polite real-world crawler validation (FILE=testdata/crawler/domains.txt)
	$(GO) run ./cmd/crawler-test --file=$(or $(FILE),testdata/crawler/domains.txt) --workers=$(or $(WORKERS),5)

doctor: ## Validate production configuration and dependencies
	$(GO) run ./cmd/doctor

release-check: ## Run the production release gate
	gofmt -l internal web cmd db | grep -q '^$$'
	$(GO) vet ./...
	$(GO) test ./...
	$(GO) test -race ./...
	$(MAKE) build
