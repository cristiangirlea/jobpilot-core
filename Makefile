.PHONY: dev test migrate seed smoke smoke-live smoke-regression smoke-mcp mcp build down logs

ifeq ($(OS),Windows_NT)
EXE := .exe
endif

dev:
	docker compose up --build

test:
	cd apps/api && go test ./...
	cd apps/mcp && go test ./...
	cd apps/web && npm run typecheck

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
	cd apps/mcp && go build -o jobpilot-mcp$(EXE) .

build:
	docker compose build

down:
	docker compose down

logs:
	docker compose logs -f api web
