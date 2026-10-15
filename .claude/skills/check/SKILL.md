---
name: check
description: Run what CI runs on JobPilot (Go checks per module, web typecheck, images, integration) before a PR
---
Run the steps in CI's order from the repository root and stop at the first failure. Report each
step's result in one line; on a failure, show the error text and fix it before re-running.

1. For `apps/api`, then `apps/mcp` (inside the module directory):

   ```bash
   test -z "$(gofmt -l .)" || { gofmt -l .; false; }
   go mod tidy -diff
   go vet ./...
   go test -race ./...
   go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
   ```

   govulncheck must be built by the module's own Go release (`go run` inside the module does that;
   a binary built by an older Go fails to load the packages). `make vulncheck` runs it for both.

   `-race` needs cgo. If `go env CGO_ENABLED` is `0` (Windows without gcc), run the tests in a
   container instead, from the repository root in Git Bash:
   `MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD":/src -w /src/apps/<module> golang:<go.mod version, e.g. 1.27.2> go test -race ./...`,
   or run `go test ./...` and say in the report that `-race` was skipped.
2. Web: `cd apps/web && npm ci && npm run typecheck`.
3. Images, only when a Dockerfile, `go.mod`, `package*.json` or `migrations/` changed:
   `docker build apps/api`, `docker build --build-arg NEXT_PUBLIC_API_URL=http://localhost:8080/api apps/web`,
   `docker build migrations`.
4. Manifests, only when `deploy/` changed: `kustomize build deploy/base | kubeconform -strict -summary -kubernetes-version 1.35.0`
   if both tools are installed; otherwise say it was left to CI. When `scripts/infra-bump*` or the
   workflow's publish or infra-bump job changed: `sh scripts/infra-bump-test.sh` (needs git and jq;
   without jq, as on Windows, run it as CI does, under dash:
   `MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD":/src:ro ubuntu:22.04 sh -c 'apt-get update -qq && apt-get install -y -qq git jq >/dev/null && sh /src/scripts/infra-bump-test.sh'`).
5. Integration, when runtime code, SQL or migrations changed. Never against the owner's stack on
   5432/8080: start a throwaway `postgres:16-alpine` on port 55432 (user, password and database
   `jobpilot`), then
   - `DATABASE_URL=postgres://jobpilot:jobpilot@localhost:55432/jobpilot?sslmode=disable sh migrations/migrate.sh`
     (needs `psql`; without it, `docker build -t jobpilot-migrate migrations` and run that image
     with `DATABASE_URL` pointing at `host.docker.internal:55432`)
   - in `apps/api`: `JOBPILOT_TEST_DATABASE_URL=<same URL> go test -count=1 -run Postgres ./...`
   - start the API natively with `API_ADDR=:18080`, that `DATABASE_URL`, a throwaway `JWT_SECRET`,
     `LLM_PROVIDER=none`, `ATS_GREENHOUSE_URL`, `ATS_ASHBY_URL` and `ATS_RECRUITEE_URL` all
     `http://localhost:18999`, `OUTBOX_SENDER_ENABLED=true` and `OUTBOX_SEND_DELAY_SECONDS=3`
   - `JOBPILOT_API_URL=http://localhost:18080/api pwsh -File scripts/smoke.ps1`, the same for
     `scripts/seniority-filter-smoke.ps1` (on Windows without `pwsh`:
     `powershell -ExecutionPolicy Bypass -File ...`)
   - in `apps/mcp`: `JOBPILOT_E2E_ATS_PORT=18999 go test -count=1 -run TestEndToEnd . -args -api http://localhost:18080`
   Stop the API by its PID and remove the container afterwards.

While iterating, run only the touched module's tests; run the whole list once before the PR.

This skill does not merge: merging is through the PR, under AGENTS.md's conditions.
