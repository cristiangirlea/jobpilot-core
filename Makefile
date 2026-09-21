GOCACHE ?= $(CURDIR)/.cache/go-build
# No tags yet, so this is a short commit SHA, or "dev" outside a checkout.
MCP_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

ifeq ($(OS),Windows_NT)
EXE := .exe
TEST_API = powershell -NoProfile -Command '$$env:GOCACHE="$(GOCACHE)"; Set-Location apps/api; go test ./...'
TEST_MCP = powershell -NoProfile -Command '$$env:GOCACHE="$(GOCACHE)"; Set-Location apps/mcp; go test ./...'
TEST_WEB = powershell -NoProfile -Command 'Set-Location apps/web; npm run typecheck'
else
TEST_API = cd apps/api && GOCACHE="$(GOCACHE)" go test ./...
TEST_MCP = cd apps/mcp && GOCACHE="$(GOCACHE)" go test ./...
TEST_WEB = cd apps/web && npm run typecheck
endif

.PHONY: dev test migrate seed smoke smoke-live smoke-regression smoke-mcp mcp build down logs

dev:
	docker compose up --build

test:
	$(TEST_API)
	$(TEST_MCP)
	$(TEST_WEB)

migrate:
	docker compose run --rm migrate

seed:
	docker compose run --rm --entrypoint /app/seed api

smoke:
	powershell -ExecutionPolicy Bypass -File scripts/smoke.ps1

smoke-live:
	powershell -ExecutionPolicy Bypass -File scripts/discovery-live.ps1

smoke-regression:
	powershell -ExecutionPolicy Bypass -File scripts/seniority-filter-smoke.ps1

smoke-mcp:
	cd apps/mcp && go test -count=1 -v -run TestEndToEnd . -args -api $(or $(JOBPILOT_E2E_API_URL),http://localhost:8080)

mcp:
	cd apps/mcp && go build -ldflags "-X main.version=$(MCP_VERSION)" -o jobpilot-mcp$(EXE) .

build:
	docker compose build

down:
	docker compose down

logs:
	docker compose logs -f api web
