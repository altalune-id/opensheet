.PHONY: help build test test-race test-cover vet fmt check generate ui-vendor buf migrate docker clean install-tools lint dev

GO      ?= go
BIN     := bin/altempl
VERSION := $(shell cat version/VERSION 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD   := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X altalune.id/template/version.Version=$(VERSION) \
	-X altalune.id/template/version.Commit=$(COMMIT) \
	-X altalune.id/template/version.BuildTime=$(BUILD)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-16s %s\n", $$1, $$2}'

build: generate ## Build the altempl binary into bin/
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/altempl

test: ## Run unit tests (fast; no external services)
	$(GO) test ./...

test-race: ## Run unit tests with -race
	$(GO) test -race ./...

test-cover: ## Run tests with coverage report
	$(GO) test -cover ./...

test-integration: ## Run integration tests. Uses TEST_PG_DSN if set, else spins ephemeral Postgres via testcontainers (docker or podman socket required).
	@if [ -n "$$TEST_PG_DSN" ]; then \
		echo "→ using TEST_PG_DSN=$$TEST_PG_DSN"; \
	else \
		echo "→ TEST_PG_DSN unset — testcontainers will spin ephemeral Postgres (needs docker/podman socket)"; \
	fi
	$(GO) test -race -tags integration ./...

test-all: test test-integration ## Unit + integration

vet: ## Run go vet
	$(GO) vet ./...

fmt: ## gofmt -w
	gofmt -w .

check: fmt vet test ## fmt + vet + test — pre-commit gate

generate: ## Regenerate templ + buf outputs (pnpm-managed buf, go-tool templ)
	$(GO) tool templ generate
	@if command -v pnpm >/dev/null 2>&1; then \
		pnpm exec buf generate; \
	else \
		echo "WARN: pnpm not on PATH — falling back to \`go tool buf generate\`. Install pnpm for reproducible plugin versions."; \
		$(GO) tool buf generate; \
	fi

ui-vendor: ## Download pinned static assets into internal/web/static
	@if [ -x scripts/ui-vendor.sh ]; then bash scripts/ui-vendor.sh; else echo "(scripts/ui-vendor.sh missing — skipping)"; fi

config-examples: ## Regenerate .env.example and config.example.yaml from config struct tags
	$(GO) tool gen-config-example
	$(GO) tool gen-config-example --format=yaml

config-examples-check: ## Fail if .env.example or config.example.yaml is stale (CI check)
	$(GO) tool gen-config-example --check
	$(GO) tool gen-config-example --format=yaml --check

tenant-tables: ## Regenerate schema/tenant_tables_gen.go from RLS migrations
	$(GO) tool gen-tenant-tables

i18n-check: ## Verify every d.Tr key in .templ files has a translation in every locale (CI check)
	$(GO) tool i18n-lint -check

i18n-diff: ## List all d.Tr/d.TrN keys used in templates
	@grep -hoE 'd\.Tr[N]?\("[^"]+"' internal/web/templates/*.templ | sed -E 's/^d\.Tr[N]?\("([^"]*)"$$/\1/' | sort -u

icons-sync: ## Refresh vendored Lucide SVGs in internal/web/icons/svg from upstream main.
	@dir=internal/web/icons/svg; \
	for f in $$dir/*.svg; do \
		name=$$(basename $$f .svg); \
		curl -fsSL -o $$f "https://raw.githubusercontent.com/lucide-icons/lucide/main/icons/$$name.svg" && echo "  ok $$name" || echo "  FAIL $$name"; \
	done

icons-add: ## Add a Lucide icon: make icons-add NAME=trash-2
	@test -n "$(NAME)" || (echo "usage: make icons-add NAME=<lucide-name>" && exit 1)
	@curl -fsSL -o internal/web/icons/svg/$(NAME).svg "https://raw.githubusercontent.com/lucide-icons/lucide/main/icons/$(NAME).svg" && echo "  added $(NAME)"

migrate: ## Run pending migrations against the configured DB
	$(GO) run ./cmd/altempl migrate up

docker: ## Build the docker image
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_TIME=$(BUILD) -t altempl:dev .

compose-up: ## Start local dev stack (postgres + mailpit + altempl) via compose.yaml
	@mkdir -p docker/data/pg
	@if command -v docker >/dev/null 2>&1; then docker compose up -d --build; \
	elif command -v podman-compose >/dev/null 2>&1; then podman-compose up -d --build; \
	else echo "neither docker nor podman-compose found"; exit 1; fi

compose-down: ## Stop and remove the local dev stack (keeps volumes)
	@if command -v docker >/dev/null 2>&1; then docker compose down; \
	elif command -v podman-compose >/dev/null 2>&1; then podman-compose down; fi

compose-logs: ## Tail logs from the local dev stack
	@if command -v docker >/dev/null 2>&1; then docker compose logs -f --tail=100; \
	elif command -v podman-compose >/dev/null 2>&1; then podman-compose logs -f --tail=100; fi

compose-nuke: ## Stop the stack AND wipe the bind-mounted postgres data
	@if command -v docker >/dev/null 2>&1; then docker compose down -v; \
	elif command -v podman-compose >/dev/null 2>&1; then podman-compose down -v; fi
	rm -rf docker/data/pg

install-tools: ## Install pinned developer tools (pnpm devDeps + go tool templ)
	@if command -v pnpm >/dev/null 2>&1; then pnpm install --frozen-lockfile; \
	else echo "pnpm not installed — install Node 24 + pnpm 11, then rerun."; exit 1; fi

lint: ## Run golangci-lint if present; else fall back to go vet
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; \
	else echo "golangci-lint not installed; running go vet instead"; $(GO) vet ./...; fi

dev: build ## Rebuild + run the server (defaults to serve)
	$(BIN) serve

clean: ## Remove build artifacts + generated code
	rm -rf bin/ dist/ gen/
	find . -name '*_templ.go' -delete
