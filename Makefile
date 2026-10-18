GOCACHE ?= $(CURDIR)/.cache/go-build
# The version CI's govulncheck step runs; keep the two in step.
GOVULNCHECK_VERSION ?= v1.9.0
# No tags yet, so this is a short commit SHA, or "dev" outside a checkout.
MCP_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

ifeq ($(OS),Windows_NT)
EXE := .exe
TEST_API = powershell -NoProfile -Command '$$env:GOCACHE="$(GOCACHE)"; Set-Location apps/api; go test ./...'
TEST_MCP = powershell -NoProfile -Command '$$env:GOCACHE="$(GOCACHE)"; Set-Location apps/mcp; go test ./...'
TEST_WEB = powershell -NoProfile -Command 'Set-Location apps/web; npm run typecheck; if ($$LASTEXITCODE -ne 0) { exit $$LASTEXITCODE }; npm test; exit $$LASTEXITCODE'
else
TEST_API = cd apps/api && GOCACHE="$(GOCACHE)" go test ./...
TEST_MCP = cd apps/mcp && GOCACHE="$(GOCACHE)" go test ./...
TEST_WEB = cd apps/web && npm run typecheck && npm test
endif

.PHONY: dev test vulncheck migrate seed smoke smoke-live smoke-regression smoke-mcp mcp build down logs staging-up staging-down staging-status

dev:
	docker compose up --build

test:
	$(TEST_API)
	$(TEST_MCP)
	$(TEST_WEB)

vulncheck:
	cd apps/api && go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...
	cd apps/mcp && go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

migrate:
	docker compose run --rm --build migrate

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

# Staging on demand (#158): a throwaway copy of REF (a branch on origin, pr/N or a commit) on
# 127.0.0.1:13000, with invented sample data; staging-down deletes it. See scripts/staging.sh.
staging-up:
	sh scripts/staging.sh up $(REF)

staging-down:
	sh scripts/staging.sh down

staging-status:
	sh scripts/staging.sh status
