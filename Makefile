.PHONY: help build test vet lint fmt generate migrate-gen migrate seed dev-up dev-down dev-reset

SHELL   := bash
COMPOSE := podman compose -f deploy/compose/compliary.yaml

# Local dev password matches the compose stack:
#   export COMPLIARY_DATABASE_PASSWORD=compliary

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | sort | awk -F':.*## ' '{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

## ── Build & quality ───────────────────────────────────────
build: ## Compile everything (no binaries left in the tree)
	@go build ./...

test: ## Run tests
	@go test ./...

vet: ## Run go vet
	@go vet ./...

lint: ## Run golangci-lint
	@golangci-lint run ./...

fmt: ## Format code + sort imports
	@golangci-lint fmt ./... 2>/dev/null || gofmt -w .

generate: ## Generate sqlc code from sql/
	@sqlc generate

## ── Database ──────────────────────────────────────────────
migrate-gen: ## Generate migrations from sql/*/schema.sql (requires Atlas CLI + running Postgres)
	@go run ./tools/migragen $(if $(name),-name $(name))

migrate: ## Apply pending migrations (goose + atlas.sum verification)
	@go run ./cmd/migrate

seed: ## Load framework registry + vocabularies from deploy/seed/*.csv
	@go run ./cmd/seed

## ── Dev stack (podman) ────────────────────────────────────
dev-up: ## Start dev stack (PostgreSQL + pgvector)
	@$(COMPOSE) up -d

dev-down: ## Stop dev stack
	@$(COMPOSE) down

dev-reset: ## Stop dev stack and remove volumes
	@$(COMPOSE) down -v

.DEFAULT_GOAL := help
