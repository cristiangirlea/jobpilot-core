# JobPilot Core (private)

Self-hosted job search assistant: discovers jobs from public APIs and employer boards, scores each
one against the user's profile and filters, drafts factual application material, and tracks
applications. A Go API on PostgreSQL, a Next.js web app, and an MCP server the owner uses daily.
The Bounty Farm is not here.

`README.md` is the canonical design document: read its "Resume work" reading order, the section
for the area you touch, and "Contributor invariants" before changing behaviour. It is long; read
the part you need. `docs/mcp-dogfood.md` logs the MCP findings and their fixes.

## Layout

| Path | Holds |
|---|---|
| `apps/api` | Go module `jobpilot/core/api`. `cmd/api` (server), `cmd/seed`; `internal/httpapi/server.go` (every route), `store` (SQL, user isolation), `matching`, `discovery`, `generator`, `ats` (employer form readers), `cv`, `answers`, `llm`, `auth` (JWT, `jpat_` agent tokens). |
| `apps/mcp` | Separate Go module `jobpilot/core/mcp`: stdio MCP server. A client of the REST API only; tools in `internal/jobpilot/tools*.go`. |
| `apps/web` | Next.js App Router, React, Tailwind. `lib/api.ts` is the client, `lib/types.ts` mirrors `apps/api/internal/models`. |
| `migrations/` | Numbered SQL files, `migrate.sh` (each file once, advisory lock) and its image. |
| `scripts/` | PowerShell smoke tests (`smoke.ps1`, `seniority-filter-smoke.ps1`, `discovery-live.ps1`) and `agent-token.ps1`; `infra-bump.sh`, CI's bump PR into infra (README, "Kubernetes"), tested by `infra-bump-test.sh`; `check-integration.sh`, `/check`'s integration step on a stack of its own; `staging.sh` (`make staging-up REF=...`, `staging-down`), staging on demand, tested by `staging-test.sh`. |
| `deploy/base/` | Kustomize base for the API and web. The lab overlay lives in the private `infra` repo (`apps/jobpilot`). |
| `Makefile`, `docker-compose.yml` | Local stack and dev commands (`make dev`, `make test`, `make mcp`, smoke targets). |

## Build and test

CI (`.github/workflows/ci.yml`) runs, per Go module (`apps/api`, `apps/mcp`):

```bash
test -z "$(gofmt -l .)"   # LF line endings are pinned in .gitattributes
go mod tidy -diff
go vet ./...
go test -race ./...       # -race needs cgo: on Windows without gcc run it in a golang container, or drop -race locally
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...   # nothing reachable; `make vulncheck`
```

Both modules name the same Go release in `go.mod` (1.27.2), which CI installs; `apps/api/Dockerfile`
builds on `golang:1.27.2-alpine` (the golang images run only their own release). Move them together.

then `npm ci && npm run typecheck` in `apps/web`; `docker build` of `apps/api`, `apps/web`
(`--build-arg NEXT_PUBLIC_API_URL=...`) and `migrations`; kustomize + kubeconform on `deploy/base`
and `sh scripts/infra-bump-test.sh` (git and jq);
and an integration job: fresh Postgres, `migrations/migrate.sh` (twice, and concurrently), the
`...Postgres` Go tests, the API, both smoke scripts, and the MCP stdio end-to-end test.
`make test` covers the Go tests and the typecheck; `/check` runs the CI steps in order.

- Tests whose subject is SQL are named `...Postgres` and skip unless `JOBPILOT_TEST_DATABASE_URL`
  names a migrated database. A new SQL query gets one.
- The MCP end-to-end test needs `-api <origin>` (or `JOBPILOT_E2E_API_URL`); its employer-form
  section also needs `JOBPILOT_E2E_ATS_PORT` with the API's `ATS_GREENHOUSE_URL`, `ATS_ASHBY_URL`
  and `ATS_RECRUITEE_URL` pointing at it.
- `make smoke-live` hits real job APIs and is deliberately not in CI.

## The owner's data

The owner's own stacks (production on a home server and Compose on
this PC's 5432 and 8080 when running) hold the owner's real profile, CV, applications and tokens.
Staging (`make staging-up`, 127.0.0.1:13000 and 18081) holds only the seed's invented profile. There
is one per clone, shared by every worktree: check `make staging-status` before `staging-up` or
`staging-down`, since another session may be using it.

- Never read, query or print real data or the profile, in tests or while debugging. Tests use
  fakes or a throwaway `postgres:16-alpine` container. Local integration runs
  (`scripts/check-integration.sh`) use a uniquely named container and free random ports, never
  5432/8080 and never fixed ports: other sessions run them at the same time.
- The smoke scripts, `make seed`, `make migrate` and the MCP end-to-end test write to whatever API
  or database they point at, and their defaults are the owner's (`localhost:8080`, and the API's
  `DATABASE_URL` defaults to `localhost:5432`): always set the target to a throwaway stack.
- Never commit `.env`, `.mcp.json` (git-ignored; it may hold a token), CV files, exports or
  anything copied from the owner's database. `.env.example` and `.mcp.json.example` hold placeholders only.

## Product rules the code enforces (keep them true)

- Only the outbox sender (`apps/api/internal/sender`, off unless `OUTBOX_SENDER_ENABLED=true`)
  submits anything, and only entries the user approved in the web app, through the employer's own
  form over plain HTTP: no browser, no CAPTCHA solving, and an entry that may have reached the
  employer is never sent again. Composing fills the outbox; approving an outbox entry is consent to
  send, so the MCP server has no `approve_outbox`, send, upload or delete tool (a test checks).
  The one exception, the owner's choice (2026-10-16), is `upload_cv`: it stores a CV in the user's
  own JobPilot, unapproved, sends nothing to an employer, and is off unless `JOBPILOT_CV_DIR` names
  the single folder it may read (README, "Deliberately absent").
- Hard-rule rejections (current or previous employer, excluded company, an active filter) are
  final: documents, applications and the applied status are refused with 409, and `apply_url` /
  `employer_url` are withheld centrally in the store scanners. Never weaken this to the frontend.
- Drafts use only facts from the stored profile and CV, never invented ones. Every AI path has a
  deterministic fallback and works with `LLM_PROVIDER=none` (the default, and the setting for tests).
- No CAPTCHA bypass, stealth automation, or LinkedIn/Indeed form submission.

## Working rules

- One branch per change, one PR against `master`. **Everything is tested before a merge**: the
  full local checks (`/check`), tests for every behaviour the change adds or alters, and CI green
  on the owner's runner. Only then may an agent merge its PR, and only through the PR (`gh pr
  merge <n> --merge --delete-branch --match-head-commit <sha>`), when an independent review ran
  and its findings are fixed, a security read of the diff has nothing open, and the head being
  merged is the commit all of that ran on. What could not be tested is said in the PR and waits
  for the owner. Never auto-merge (`--auto`) or `--admin`, never push to `master`, never tag or
  deploy. A merge, push or manual workflow run on `master` publishes images to the lab registry
  and moves infra's bump PR (merging that one is the deploy), so each merge also needs the
  owner's yes in chat first, and a manual run stays the owner's step.
- Commit messages and PR text: plain sentences, optionally an area first (`Migrations: ...`,
  `CI: ...`). No conventional-commit prefixes, no emoji, no AI attribution lines. Commit as
  `maintainer@example.com` (check `git config user.email`).
- CI runs only on the owner's runner (`vars.CI_RUNNER`, the lab cluster); `publish` runs only
  there; without `CI_RUNNER` every job is skipped (jobpilot#71 removed the `ubuntu-latest`
  fallbacks). Never add a GitHub-hosted runner or a fallback to one.
- Keep the Makefile and `ci.yml` in step (the workflow's header says so).
- Other sessions merge concurrently: migration and PR numbers can be taken between branch and merge.
  Check `ls migrations` and merge `master` into the branch before the checks that precede a merge.
- Secrets (`JWT_SECRET`, tokens, database passwords beyond Compose's dev ones) are never read,
  printed or committed.
- Small diffs: don't restyle what you didn't change. Not sure? Ask, with one proposal.
