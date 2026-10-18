---
name: check
description: Run what CI runs on JobPilot (Go checks per module, web typecheck and tests, images, integration) before a PR
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
2. Web: `cd apps/web && npm ci && npm run typecheck && npm test`.
3. Images, only when a Dockerfile, `go.mod`, `package*.json` or `migrations/` changed:
   `docker build apps/api`, `docker build --build-arg NEXT_PUBLIC_API_URL=http://localhost:8080/api apps/web`,
   `docker build migrations`.
4. Manifests, only when `deploy/` changed: `kustomize build deploy/base | kubeconform -strict -summary -kubernetes-version 1.35.0`
   if both tools are installed; otherwise say it was left to CI. When `scripts/infra-bump*` or the
   workflow's publish or infra-bump job changed: `sh scripts/infra-bump-test.sh` (needs git and jq;
   without jq, as on Windows, run it as CI does, under dash:
   `MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD":/src:ro ubuntu:22.04 sh -c 'apt-get update -qq && apt-get install -y -qq git jq >/dev/null && sh /src/scripts/infra-bump-test.sh'`).
   When `scripts/staging*` or `deploy/staging/` changed (CI runs it in the images job):
   `sh scripts/staging-test.sh` (needs git; Docker Compose 2.24+ for the rendered part, which it
   skips without Compose).
5. Integration, when runtime code, SQL or migrations changed: `bash scripts/check-integration.sh`
   from the repository root (Git Bash on Windows; it takes a few minutes, so run it in the
   background where a tool call has a time limit). Other sessions run it at the same time from
   their own worktrees, so it never uses fixed ports and never touches the owner's stack on
   5432/8080. Each run:
   - starts a `postgres:16-alpine` container named `jobpilot-check-<time>-<pid>-<random>` on a
     loopback port Docker picks, and applies `migrations/migrate.sh` with the container's own `psql`;
   - runs `go test -count=1 -run Postgres ./...` in `apps/api` against it;
   - builds the API binary into a temporary directory and starts it on `127.0.0.1:<free port>`
     with the fake ATS on another free port (random in 20000-39999, checked with `ss` or
     `Get-NetTCPConnection`), `LLM_PROVIDER=none`, `REGISTRATION=open` (the scripts create their
     own accounts), `OUTBOX_SENDER_ENABLED=true` and `OUTBOX_SEND_DELAY_SECONDS=3`;
   - before each smoke script and around the MCP end-to-end test, confirms the only listener on
     the API's port runs the binary this run built (its unique path; in Git Bash `$!` is a bash
     stub, not `api.exe`, so a PID comparison would not do), its log says it listens there, and
     it answers `/api/health`. If another session's API answers instead, it stops there rather
     than testing someone else's branch and data;
   - runs `scripts/smoke.ps1`, `scripts/seniority-filter-smoke.ps1` (`pwsh`, or Windows
     `powershell` without it) and `TestEndToEnd` in `apps/mcp`;
   - stops its API (by that path on Windows) and removes its container on exit, also on failure,
     and prints where the API log is.
   "The API exited" or "cannot listen on port" means another process took a picked port between
   the check and the bind: run it again. When running the steps by hand instead, keep the same
   rules, and under `MSYS_NO_PATHCONV=1` give a Windows `go build -o` a Windows path
   (`cygpath -w`): there `/c/x` is taken literally and lands in `<drive>:\c\x`.

While iterating, run only the touched module's tests; run the whole list once before the PR.

This skill does not merge: merging is through the PR, under AGENTS.md's conditions.
