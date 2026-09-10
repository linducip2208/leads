# LeadForge dev commands. Run from the repository root.
# Windows (PowerShell): use the same targets, e.g. `make dev`.

GO ?= go
CSS_SRC = web/assets/css/app.css
CSS_OUT = static/css/app.css
# Standalone Tailwind v4 binary (dev-machine local, gitignored).
# Download from https://github.com/tailwindlabs/tailwindcss/releases
# and place it at tools/bin/tailwindcss.exe (Windows) or tools/bin/tailwindcss.
TAILWIND ?= tools/bin/tailwindcss.exe

.PHONY: setup dev build test vet migrate migrate-down worker scheduler templ css seed

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
	$(GO) build -o bin/ ./cmd/server ./cmd/worker ./cmd/scheduler ./cmd/migrate

test: ## Run all tests
	$(GO) test ./...

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
