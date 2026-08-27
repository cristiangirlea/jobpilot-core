GOCACHE ?= $(CURDIR)/.cache/go-build

ifeq ($(OS),Windows_NT)
TEST_API = powershell -NoProfile -Command '$$env:GOCACHE="$(GOCACHE)"; Set-Location apps/api; go test ./...'
TEST_WEB = powershell -NoProfile -Command 'Set-Location apps/web; npm run typecheck'
else
TEST_API = cd apps/api && GOCACHE="$(GOCACHE)" go test ./...
TEST_WEB = cd apps/web && npm run typecheck
endif

.PHONY: dev test migrate seed smoke smoke-live smoke-regression build down logs

dev:
	docker compose up --build

test:
	$(TEST_API)
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

build:
	docker compose build

down:
	docker compose down

logs:
	docker compose logs -f api web
