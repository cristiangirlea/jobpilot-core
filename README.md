# JobPilot Core

JobPilot Core is a self-hosted job discovery, matching, application-writing, and application-tracking MVP. It discovers jobs from supported public APIs, stores each vacancy once in a shared catalog, evaluates that vacancy independently for every user, blocks applications to excluded employers, generates factual application materials without an LLM, and tracks the application lifecycle.

This README is the canonical continuation document for the project. A future developer or Codex session should be able to read this file, inspect only the files named here, and resume work without rediscovering the architecture.

Last verified: **2026-06-19**.

## Current status

The full local stack is implemented and usable:

- Registration and login with protected, user-isolated data.
- Manual profile, experience, skills, projects, and company exclusions.
- Multiple desired seniority levels.
- Reusable filters with positive (OR) and negative (NOT) title and technology rules.
- Shared job discovery through Remotive, Arbeitnow, Greenhouse, Lever, Ashby, and SmartRecruiters.
- Per-user filtering, rejection explanations, warnings, skill gaps, and 0-100 scoring.
- Deterministic recruiter messages, cover letters, candidate summaries, and common-question answers.
- Application creation, status changes, event history, and outbound-click attribution.
- Strict application lockout for rejected jobs.
- Dashboard metrics and recommended jobs.
- Docker Compose, migrations, seed command, unit tests, and end-to-end smoke scripts.
- An MCP server (`apps/mcp`) that lets agent hosts such as Claude Code read matches, generate documents, and track applications through the same API locks.

The product works without Redis and without an LLM API key. Manual job import remains available as a fallback, but discovery is the primary workflow.

## Product boundaries

JobPilot Core is an application assistant and tracker, not a job-board attack bot.

It intentionally does **not**:

- Submit forms on LinkedIn, Indeed, or other job boards.
- Run stealth browser automation.
- Bypass CAPTCHA or anti-bot systems.
- Scrape arbitrary HTML pages.
- Invent experience, achievements, skills, or qualifications.
- Append fabricated referral codes to employer links.
- Guarantee referral revenue merely because an outbound click was recorded.

An external submission integration should only be added later for an official, authorized employer or ATS API with explicit user consent. The current app prepares the application, opens the real apply URL, and lets the user record the result.

## Quick start

### Requirements

- Docker Desktop with Docker Compose v2.
- Make, or the ability to run the equivalent commands from `Makefile`.
- Ports `3000`, `8080`, and `5432` available.

### Run the stack

```bash
cp .env.example .env
# Replace JWT_SECRET in .env with a long random value.
make dev
```

On PowerShell, if `cp` or `make` is unavailable:

```powershell
Copy-Item .env.example .env
docker compose up --build
```

Open:

- Web UI: [http://localhost:3000](http://localhost:3000)
- API health: [http://localhost:8080/api/health](http://localhost:8080/api/health)
- PostgreSQL: `localhost:5432`

On first startup, Compose builds the API and web images, waits for PostgreSQL, runs every migration, and starts the services. PostgreSQL data persists in the `jobpilot_postgres` Docker volume.

### First-use workflow

1. Register a user.
2. Complete **Profile > Basics**, including desired titles and one or more seniority levels.
3. Add current and previous employers under **Experience**. These employers are automatically excluded.
4. Add skills and projects. These are used for scoring and generated text.
5. Add any extra excluded companies under **Ignored companies**.
6. Create one active filter preset.
7. Open **Discovery** and select **Find jobs for my profile**, or configure employer-specific sources.
8. Review accepted and rejected jobs, score details, warnings, matched skills, and missing skills.
9. Generate or review application materials.
10. Create an application, manually open the apply URL, submit it yourself, and update its status.

## Resume work in a future session

Give a new coding session this prompt:

```text
Continue JobPilot Core from the existing repository. Read README.md first; it is the canonical architecture and continuation document. Preserve the shared-job/per-user-match model, exclusion safety rules, deterministic generation, and no-job-board-automation boundary. Inspect git status before editing, implement the requested change, and run the relevant commands from the Verification section.
```

Recommended reading order:

1. `README.md` - product contract and current state.
2. `apps/api/internal/models/models.go` - API/domain data shapes.
3. `apps/api/internal/httpapi/server.go` - all REST routes and handlers.
4. `apps/api/internal/store/store.go` - PostgreSQL queries and user isolation.
5. `apps/api/internal/matching/matching.go` - rejection and scoring rules.
6. `apps/api/internal/discovery/service.go` - discovery orchestration and scheduler.
7. `apps/api/internal/discovery/fetcher.go` - supported provider adapters.
8. `apps/web/lib/api.ts`, `apps/web/components`, and the affected page.
9. `apps/mcp/internal/jobpilot/tools.go` - the agent-facing tool set over the REST API.
10. The latest SQL migration plus any migration touching the relevant entity.

Before changing anything, run:

```bash
git status --short
make test
```

Do not assume uncommitted changes are disposable. They may belong to the user.

## Architecture

```text
Browser (Next.js + React + Tailwind)     Agent host (Claude Code)
              |                                    |
              |                          stdio MCP |
              |                                    v
              |                          jobpilot-mcp (Go, apps/mcp)
              |                                    |
              | JSON REST + bearer JWT             | JSON REST + bearer JWT
              v                                    v
Go API (net/http)
  |-- auth and validation
  |-- profile/filter/application services
  |-- matching and deterministic generation
  |-- discovery scheduler and provider adapters
              |
              | pgx
              v
PostgreSQL
  |-- global companies and jobs
  |-- per-user profiles, matches, filters, applications, and documents
```

Redis is not used. Discovery jobs are intentionally small and run in the API process. PostgreSQL is the only required stateful service. The MCP server is a second client of the API, not a second back end: it holds no state and never touches the database.

### Repository map

```text
apps/
  api/
    cmd/api/main.go                    API startup and graceful shutdown
    cmd/seed/main.go                   sample-data seed command
    internal/auth/                     bcrypt and JWT
    internal/company/normalize.go      company names, aliases, and domains
    internal/config/config.go          environment configuration
    internal/db/db.go                  PostgreSQL connection
    internal/discovery/fetcher.go      provider adapters
    internal/discovery/service.go      runs, scheduler, dedup, auto-prepare
    internal/generator/generator.go    deterministic application text
    internal/httpapi/server.go         REST router and handlers
    internal/matching/matching.go      hard filters and weighted score
    internal/models/models.go          shared domain models
    internal/store/store.go            SQL persistence and isolation
  web/
    app/                               Next.js App Router pages
    components/                        navigation, forms, chips, UI helpers
    lib/api.ts                         authenticated API client
    lib/types.ts                       frontend data types
  mcp/                                 separate Go module (Go 1.25)
    main.go                            stdio MCP server entrypoint
    internal/jobpilot/client.go        REST client: bearer token or login
    internal/jobpilot/tools.go         tool set and host instructions
    internal/jobpilot/types.go         wire mirrors and agent-facing summaries
    e2e_test.go                        stdio test against a running API
migrations/                            idempotent PostgreSQL migrations
scripts/                               PowerShell smoke and regression tests
docker-compose.yml                     local PostgreSQL, migrations, API, web
Makefile                               common developer commands
.env.example                           required local secret template
```

## Data ownership and isolation

Jobs are deliberately **not** owned by one user. Two users may match and apply to the same job without duplicating the global vacancy.

### Global catalog data

- `companies`
- `jobs`

A SHA-256 fingerprint deduplicates a job using normalized company, normalized title, location, and the apply URL host/path. `jobs.user_id` is nullable and retained only as a legacy creator/import field.

### Private per-user data

- `user_profiles`
- `work_experiences`
- `skills`
- `projects`
- `company_blacklist`
- `filter_presets`
- `job_matches`
- `applications`
- `application_documents`
- `application_events`
- `discovery_settings`
- `discovery_sources`
- `discovery_runs`
- `outbound_clicks`

Every job visible to a user is reached through that user's `job_matches` row. Profiles, filters, match explanations, documents, applications, and history are always scoped by `user_id` in the store layer.

## Implemented pages

| Route | Purpose |
|---|---|
| `/register` | Create an account |
| `/login` | Authenticate |
| `/dashboard` | Completeness, catalog and application metrics, top recommendations, next actions |
| `/profile` | Profile overview |
| `/profile/setup` | Contact, job preferences, seniorities, authorization, compensation, notice |
| `/profile/experience` | Current and previous employers and work history |
| `/profile/skills` | Candidate skills |
| `/profile/projects` | Candidate projects |
| `/profile/blacklist` | Extra excluded companies |
| `/filters` | Active filters, include/exclude titles and technologies, company exclusions |
| `/discovery` | Discovery settings, sources, validation, manual runs, recommended search |
| `/jobs` | All matched jobs, including visibly rejected jobs |
| `/jobs/import` | Manual import fallback |
| `/jobs/[id]` | Job details, match explanation, safety state, generated documents |
| `/matches` | Accepted recommendations only |
| `/matches/[id]` | Detailed match breakdown |
| `/applications` | Application pipeline |
| `/applications/[id]` | Documents, outbound apply action, status, and history |
| `/settings` | Account and product settings |

Authentication tokens are stored in browser `localStorage`. The API client sends them as bearer tokens; a `401` clears the token and redirects to login.

## Candidate profile

The profile is filled manually. CV parsing is intentionally deferred.

Supported data includes:

- Name, email, phone, location.
- LinkedIn, GitHub, and portfolio URLs.
- Current title and desired job titles.
- Multiple desired seniority levels.
- Years of experience.
- Skills, work experience, projects, certifications, and languages.
- Salary or rate expectations and currency.
- Remote, hybrid, or onsite preference.
- Preferred countries and time zones.
- Work authorization and notice period.
- Current employer, previous employers, and additional ignored companies.

The profile model still accepts the legacy singular `seniority` field for compatibility, but new UI and matching logic use `seniorities[]`.

## Company exclusion and application safety

Company exclusion is a safety invariant, not merely a search preference.

A job is hard-rejected when its company exactly matches:

- The user's current employer.
- Any previous employer.
- A manually blacklisted company.

The normalizer:

- Lowercases and trims.
- Removes punctuation.
- Removes common legal suffixes: `ltd`, `limited`, `llc`, `inc`, `incorporated`, `gmbh`, `srl`, `s.r.l`, `sa`, `s.a`, `bv`, `plc`, `ag`.
- Collapses whitespace.
- Compares recognized aliases such as AKA, DBA, and formerly-known-as forms.
- Compares normalized domains when domains are available.

An exact normalized name, alias, or domain match rejects. Fuzzy similarity at or above the high threshold (currently `0.88`) produces a warning for human review; fuzzy matching alone does not hard-reject.

Rejected jobs remain visible in `/jobs` so the user can understand the decision, but they are removed from `/matches` and all application actions are locked:

- No outbound apply URL.
- No generated documents.
- No new application record.
- No transition of an existing draft to `applied` after the job becomes rejected.

Already-applied historical records and existing documents are retained for audit history. The backend is authoritative and returns HTTP `409 Conflict` for a blocked application action; hiding a UI button is not considered sufficient enforcement.

Saving the profile or adding/removing a blacklist entry re-evaluates existing jobs immediately.

## Filters

Filter presets support:

- Included job titles: **OR** semantics; at least one should match.
- Excluded title terms: **NOT** semantics; any match rejects.
- Required skills.
- Included technologies: **OR** semantics; any listed technology is enough.
- Excluded technologies: **NOT** semantics; any match rejects.
- Multiple seniorities through the profile.
- Allowed locations.
- Allowed remote types.
- Minimum known salary/rate and currency.
- Allowed employment types.
- Excluded industries.
- Maximum posting age.

Hard rejection occurs for an excluded title, excluded technology, incompatible location or remote policy, known compensation below the minimum, incompatible employment type, excluded industry, or an over-age posting.

Unknown job metadata usually produces a warning rather than a false rejection. A remote role with a permitted remote policy but ambiguous geographic eligibility also warns rather than rejecting only from text mismatch.

Saving or deleting a filter preset re-evaluates existing jobs. The frontend represents include/OR rules as positive chips and exclude/NOT rules as negative chips.

## Job discovery

Discovery is the primary ingestion method. Manual import exists for sources not yet supported.

### Supported providers

| Provider | Configuration | Behavior and limits |
|---|---|---|
| Remotive | Optional focused search query | Public remote-jobs API; up to 100 jobs per request |
| Arbeitnow | Optional OR query and NOT query | Public Europe-focused feed; currently processes the first page and filters title, description, and tags locally |
| Greenhouse | Employer board token | Public board API; token is the part after `job-boards.greenhouse.io/` or `boards.greenhouse.io/` |
| Lever | Employer site slug | Public postings API for one employer |
| Ashby | Employer board name | Public posting API for one employer |
| SmartRecruiters | Employer/company identifier | Public company postings API; currently capped at 25 plus detail requests |

Employer-direct sources tend to have cleaner provenance and less aggregation churn. Source validation fetches a small sample before saving so a board token or company identifier can be checked.

There is no generic HTML crawler. New providers should be implemented as explicit adapters in `apps/api/internal/discovery/fetcher.go`, use documented/public endpoints, have bounded timeouts and result counts, and include tests.

### OR and NOT discovery terms

Discovery sources have two independent query fields:

- `search_query`: positive OR terms, shown as green chips.
- `excluded_search_query`: negative NOT terms, shown as red chips.

For example, positive terms `Go`, `Golang`, `Backend` allow a job matching any of them. Negative terms `PHP`, `WordPress` discard a job matching either. Provider capabilities differ, so adapters may apply some filtering locally after fetching bounded results.

### Recommended discovery

**Find jobs for my profile** derives a focused search from the first desired title, or the current title if no desired title exists. It ensures that a Remotive source exists, then runs every active source. This is a convenience action; matching still applies the full profile, blacklist, and active filter to every returned job.

In the UI, **Related to me** means an accepted match with score at least `70`.

### Scheduler and run behavior

- The scheduler runs inside the Go API process.
- It checks for due users once per minute.
- It claims at most 10 due users with PostgreSQL `FOR UPDATE SKIP LOCKED`.
- The minimum supported schedule interval is six hours (`360` minutes).
- A run is capped at 500 jobs.
- A process-local mutex permits only one discovery run at a time.
- Source failures are stored on the run; a run fails completely only when every active source fails.
- Global fingerprints deduplicate jobs across users, providers, and repeated runs.
- Every discovered job gets a private match evaluation for the requesting user.

This is intentionally low-resource architecture for one local API instance. The process-local mutex is not sufficient for a horizontally scaled multi-instance deployment; use a PostgreSQL advisory lock or a real work queue before scaling out.

### Auto-prepare

When enabled, accepted jobs at or above the configured minimum score automatically receive:

- A new application record.
- Deterministic application documents.
- Application status `ready`.

Auto-prepare never opens a browser or submits an external application.

## Matching and scoring

Each user receives an independent evaluation for a shared job.

### Score weights

| Component | Maximum points |
|---|---:|
| Title match | 25 |
| Skill match | 30 |
| Seniority match | 15 |
| Location and remote match | 10 |
| Salary or rate match | 10 |
| Domain or industry match | 5 |
| Freshness | 5 |
| **Total** | **100** |

Multiple desired seniorities use the best matching seniority; the score is not diluted by averaging several choices.

### Evaluation output

Every match stores:

- Accepted or rejected state.
- Total score and component breakdown.
- Human-readable match reasons.
- Rejection reasons.
- Matched skills.
- Missing skills.
- Red flags and warnings.

Rejected jobs retain their calculated score for diagnosis and audit, but a high score never overrides a hard rejection.

The main matching implementation is `apps/api/internal/matching/matching.go`. Company normalization is isolated in `apps/api/internal/company/normalize.go`.

## Application generation

Generation is deterministic and requires no API key. It produces:

1. A short recruiter message.
2. A cover letter.
3. A custom candidate summary.
4. Answers to:
   - Tell us about yourself.
   - Why are you interested in this role?
   - Why are you a good fit?
   - Salary expectation.
   - Notice period.
   - Work authorization.
   - Remote work preference.

The generator may only use data already present in the profile, skills, projects, work experience, and computed match. It must never infer or invent experience. Copy buttons are available in the UI.

There is currently no LLM integration. If one is added later, deterministic generation must remain the default fallback, input facts must be constrained to the same source data, and generated claims must be validated or clearly presented for user review.

## Applications and referral attribution

Application statuses are:

- `draft`
- `ready`
- `applied`
- `interview`
- `rejected`
- `offer`
- `withdrawn`

Every status change creates an `application_events` history row.

Apply buttons use the protected `POST /api/jobs/{id}/outbound` endpoint. It records user, job, and optional application attribution. The endpoint returns a partner destination only when an authorized partner URL is configured; otherwise it returns the original employer apply URL. It is blocked for rejected jobs.

Click attribution alone does not create revenue. Monetization requires a real employer, affiliate, CPA, recruiting, or hiring-partner agreement plus suitable user disclosure. Do not create or guess third-party referral codes.

## REST API

All request and response bodies are JSON. Protected endpoints require:

```http
Authorization: Bearer <token>
```

### Public endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/health` | Health check |
| `POST` | `/api/auth/register` | Create user and return token |
| `POST` | `/api/auth/login` | Authenticate and return token |

### Profile endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET`, `PUT` | `/api/profile` | Read or save the main profile |
| `GET`, `POST` | `/api/experiences` | List or add work experience |
| `DELETE` | `/api/experiences/{id}` | Remove work experience |
| `GET`, `POST` | `/api/skills` | List or add skills |
| `DELETE` | `/api/skills/{id}` | Remove a skill |
| `GET`, `POST` | `/api/projects` | List or add projects |
| `DELETE` | `/api/projects/{id}` | Remove a project |
| `GET`, `POST` | `/api/blacklist` | List or add excluded companies |
| `DELETE` | `/api/blacklist/{id}` | Remove an excluded company |

### Filter, job, and match endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET`, `POST` | `/api/filters` | List or create filter presets |
| `PUT`, `DELETE` | `/api/filters/{id}` | Save or delete a filter preset |
| `GET`, `POST` | `/api/jobs` | List matched jobs or manually import one |
| `GET` | `/api/jobs/{id}` | Job and user-specific match details |
| `POST` | `/api/jobs/{id}/generate` | Generate factual application documents |
| `POST` | `/api/jobs/{id}/outbound` | Record attribution and return allowed apply URL |
| `GET` | `/api/matches` | List accepted matches |
| `GET` | `/api/matches/{id}` | Read one match |

### Discovery endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET`, `PUT` | `/api/discovery/settings` | Read or save scheduler and auto-prepare settings |
| `GET`, `POST` | `/api/discovery/sources` | List or create sources |
| `DELETE` | `/api/discovery/sources/{id}` | Remove a source |
| `POST` | `/api/discovery/validate` | Test a provider configuration and return sample jobs |
| `GET` | `/api/discovery/runs` | Run history and provider errors |
| `POST` | `/api/discovery/run` | Run all active sources now |
| `POST` | `/api/discovery/recommended` | Derive a profile-focused search and run active sources |

### Application and dashboard endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET`, `POST` | `/api/applications` | List or create applications |
| `GET` | `/api/applications/{id}` | Application, documents, and history |
| `PATCH` | `/api/applications/{id}` | Update status and application data |
| `GET` | `/api/dashboard` | User-specific metrics and recommendations |

The HTTP layer uses strict JSON decoding with unknown fields rejected and a 2 MiB request-body limit. See `apps/api/internal/httpapi/server.go` for exact payload structs and validation messages.

## MCP server

`apps/mcp` is a Model Context Protocol server that lets an agent host such as Claude Code work with JobPilot through typed tools. It is a client of the REST API and nothing more: every call carries the user's bearer token, so user isolation, the rejection locks, and the no-submission boundary are enforced by the API exactly as they are for the web UI.

It is its own Go module because the MCP SDK requires Go 1.25 and brings dependencies the API does not need. The API module and its Go 1.23 container build are unchanged.

### Build and register

```bash
make mcp
claude mcp add jobpilot -e JOBPILOT_TOKEN=<token> -- /absolute/path/to/apps/mcp/jobpilot-mcp
```

Get a token from `POST /api/auth/login`, or let the server log in for you:

| Variable | Purpose |
|---|---|
| `JOBPILOT_API_URL` | API origin, default `http://localhost:8080`; a trailing `/api` is accepted |
| `JOBPILOT_TOKEN` | Bearer token from login or registration |
| `JOBPILOT_EMAIL`, `JOBPILOT_PASSWORD` | Alternative to a token; the server logs in on first use and once more after a `401`, which covers token expiry. Prefer a token: values passed with `claude mcp add -e` are stored in plaintext in the host's config, and a password never expires |

The transport is stdio, so the binary logs to stderr only.

### Tools

| Tool | Kind | Purpose |
|---|---|---|
| `get_profile` | read | Candidate facts the agent may use when writing |
| `list_jobs` | read | Accepted (default), rejected, or all evaluated jobs as summaries, with `min_score` and `limit` |
| `get_job` | read | Full posting plus score breakdown, reasons, warnings, and rejection reasons |
| `list_applications` | read | Applications with their job, optionally filtered by status |
| `get_application` | read | One application with its generated documents and blocked state |
| `get_dashboard` | read | Pipeline metrics, top matches, and next actions |
| `list_discovery_runs` | read | Recent run history |
| `generate_documents` | write | Deterministic documents for a job; refused for rejected jobs |
| `create_application` | write | Draft application record; idempotent; refused for rejected jobs |
| `update_application_status` | write | Status change with history; keeps existing notes unless new notes are passed |
| `run_discovery` | write | Run every active source now |

List tools return summaries without the job description so a page of matches stays small in the agent's context; `get_job` returns the full text. The `apply_url` is blanked for rejected jobs and blocked applications, as the web UI does. Server instructions tell the host that rejected jobs are locked, that nothing here submits or records an apply click, and that generated text may only use stored facts. A `409` from the API is returned as a tool error with the rejection hint, so the model can explain it instead of retrying.

Deliberately absent: `POST /api/jobs/{id}/outbound` (an agent must not fire attribution clicks), registration, deletes, and profile or filter edits.

### Verification

```bash
cd apps/mcp && go test ./...   # unit tests against a fake API; no stack needed
make smoke-mcp                 # builds the binary and drives it over stdio against http://localhost:8080
```

## Database and migrations

PostgreSQL 16 is used in Compose. Migrations are plain, idempotent SQL and are rerun safely by the `migrate` service.

| Migration | Purpose |
|---|---|
| `001_init.sql` | Authentication, profile, companies, jobs, matches, applications, documents, and events |
| `002_seed_demo.sql` | Seed documentation/placeholder; no shared default account |
| `003_discovery.sql` | Global job catalog, discovery settings/sources/runs, fingerprints, outbound attribution |
| `004_boards_and_technology_filters.sql` | Additional board providers and include/exclude technology filters |
| `005_profile_seniorities.sql` | Multiple profile seniorities and legacy migration |
| `006_discovery_exclusions.sql` | Source-level NOT queries and source uniqueness rules |

Core entities:

```text
users
user_profiles
work_experiences
skills
projects
company_blacklist
filter_presets
companies
jobs
job_matches
applications
application_documents
application_events
discovery_settings
discovery_sources
discovery_runs
outbound_clicks
```

When changing schema:

1. Add a new numbered migration; do not rewrite a migration already applied to persistent environments unless correcting a development-only mistake deliberately.
2. Make it safe to rerun where practical (`IF NOT EXISTS`, guarded constraints, conflict handling).
3. Update Go models and store queries together.
4. Update frontend types and forms.
5. Run `make migrate`, `make test`, and the relevant smoke script.

## Configuration

### API environment

| Variable | Compose/default behavior |
|---|---|
| `API_ADDR` | Defaults to `:8080` |
| `DATABASE_URL` | Defaults to a local PostgreSQL connection in native development; Compose supplies its service URL |
| `JWT_SECRET` | Required secret; set it in `.env` |
| `FRONTEND_URL` | Allowed CORS origin; Compose uses `http://localhost:3000` |
| `TOKEN_TTL_HOURS` | Defaults to `168` (7 days) |

### Frontend environment

| Variable | Compose/default behavior |
|---|---|
| `NEXT_PUBLIC_API_URL` | API base URL; Compose uses `http://localhost:8080` |

No LLM, Redis, Kafka, Elasticsearch, or Kubernetes configuration is required.

## Development commands

```bash
make dev              # build and run PostgreSQL, migrations, API, and web
make test             # Go tests (API and MCP) and frontend TypeScript typecheck
make migrate          # rerun SQL migrations
make seed             # add sample data to the first registered user
make smoke            # full local API workflow
make smoke-live       # capped live Remotive discovery and auto-prepare flow
make smoke-regression # seniority, reevaluation, OR/NOT, blacklist, and lockout checks
make smoke-mcp        # drive the MCP server over stdio against the running API
make mcp              # build apps/mcp/jobpilot-mcp
make build            # build production containers
make logs             # follow API and web logs
make down              # stop services without deleting persistent data
```

Run `make seed` only after registering at least one user. The seed command deliberately does not create a known default password.

### Native backend

Requires Go 1.23 and PostgreSQL:

```bash
cd apps/api
go mod download
go test ./...
go run ./cmd/api
```

### Native MCP server

Requires Go 1.25. The unit tests run against a fake API; only the binary itself needs a running API:

```bash
cd apps/mcp
go test ./...
go build -o jobpilot-mcp .
```

### Native frontend

The container uses Node.js 22. For local development:

```bash
cd apps/web
npm install
npm run dev
```

Useful frontend checks:

```bash
npm run typecheck
npm run build
```

## Verification

The minimum check for ordinary changes is:

```bash
make test
```

For backend behavior changes with the stack running:

```bash
make smoke
make smoke-regression
```

For discovery/provider changes:

```bash
make smoke-live
```

For MCP server changes with the stack running:

```bash
make smoke-mcp
```

The test suites cover:

- JWT/password behavior.
- Company normalization, aliases, domains, and fuzzy warnings.
- Matching weights and score bounds.
- Hard filter and blacklist behavior.
- Multiple seniority choices.
- Discovery parsing and query inclusion/exclusion behavior.
- Deterministic generation without invented profile data.
- Full authentication/profile/filter/job/document/application/outbound API flow.
- Shared catalog behavior across users.
- Re-evaluation after profile, filter, and blacklist changes.
- Application action lockout for rejected companies/jobs.
- MCP tool catalog, summaries, filters, blocked-action errors, and notes preservation against a fake API, plus a stdio end-to-end flow against a live API.

At the last verification point, Go tests, TypeScript typechecking, the production web build, the main smoke flow, and the regression smoke flow passed.

## Security and reliability notes

Implemented safeguards:

- bcrypt password hashing.
- Signed, expiring JWT bearer tokens.
- Protected routes and user-scoped SQL access.
- Strict JSON parsing and request size limits.
- Exact configured CORS origin.
- Bounded provider requests and per-run limits.
- PostgreSQL transactions and constraints for core state changes.
- Backend-enforced rejection/apply locks.
- Graceful API shutdown and startup database retries.

Local-MVP limitations:

- JWTs are stored in `localStorage`, not secure HTTP-only cookies.
- Local Compose uses HTTP and a development database password.
- There is no email verification, password reset, MFA, rate limiter, or session revocation list.
- The scheduler lock is process-local and is intended for one API instance.
- Discovery APIs can change or rate-limit without notice.
- This deployment is not hardened for an internet-facing production environment.

Before public production use, add secure cookies or another hardened token strategy, TLS, secret management, rate limiting, account recovery, audit/observability, backups, provider terms review, privacy controls, and a cross-instance scheduler lock.

## Known limitations and best next tasks

These are known gaps, not hidden surprises:

1. **Re-evaluation triggers:** saving the main profile, filters, or blacklist re-evaluates jobs. Adding/removing a skill, project, or experience does not currently rescore every existing match immediately. Resaving the profile/filter or rerunning discovery refreshes it. A centralized `ReevaluateUserJobs` trigger should be added to those mutations.
2. **Provider pagination:** Arbeitnow currently uses the first page, SmartRecruiters is capped at 25, and Remotive at 100. Add bounded pagination only with clear per-run limits and cancellation.
3. **Source management UI:** sources can be added, validated, and deleted. Rich editing, enable/disable toggles, and per-source schedules can be improved.
4. **Catalog scale:** job and match lists need stronger pagination/search/index review before very large catalogs.
5. **LLM enhancement:** no optional LLM adapter is implemented. Any future adapter must preserve the deterministic fallback and factuality boundary.
6. **Referral operations:** outbound attribution exists, but there is no partner administration, agreement workflow, payout ledger, or disclosure management UI.
7. **Production auth and operations:** see the security section above.
8. **Official application APIs:** external submission is intentionally absent. Add only authorized ATS/employer integrations, never generic stealth automation.

Suggested priority order:

1. Rescore after experience/skill/project mutations.
2. Add integration tests around store/HTTP authorization and rejected-job locks.
3. Improve source editing/toggling and provider diagnostics.
4. Add bounded pagination plus job archival/expiration.
5. Harden authentication and operations for deployment.
6. Add partner administration only after a real commercial agreement exists.

## Contributor invariants

Preserve these rules when extending the product:

- A job is global; a match is per user.
- Every private query is scoped by authenticated `user_id`.
- A hard rejection always wins over score.
- Rejected jobs may be visible for explanation but cannot be applied to.
- Backend authorization and safety checks must not depend on the frontend.
- Current employers, previous employers, and manual blacklist entries are hard exclusions.
- Positive title/technology selections use OR; negative selections use NOT.
- Generated claims come only from stored candidate facts.
- The application must continue to work with no LLM key.
- Provider fetches are explicit, bounded, cancellable, and terms-conscious.
- Do not add CAPTCHA bypass, stealth submission, LinkedIn/Indeed automation, Kafka, Kubernetes, or Elasticsearch to this MVP.
- Keep PostgreSQL as the source of truth; introduce another service only for a demonstrated need.
- The MCP server is a client of the REST API: it never reads the database, never bypasses the API's authorization, and never exposes outbound attribution or any submission action.

## Troubleshooting

### Check service state and logs

```bash
docker compose ps
make logs
```

### Re-run migrations

```bash
make migrate
```

Migrations are idempotent. A normal restart should not require deleting data.

### Rebuild after dependency or Dockerfile changes

```bash
docker compose up --build
```

### API is healthy but the browser cannot call it

Check that `NEXT_PUBLIC_API_URL` points to a URL reachable by the browser, normally `http://localhost:8080`, and that `FRONTEND_URL` exactly matches the browser origin, normally `http://localhost:3000`.

### Login unexpectedly returns to `/login`

Inspect the API response and browser storage. A `401` intentionally clears the stored token. Tokens expire after `TOKEN_TTL_HOURS`, and changing `JWT_SECRET` invalidates existing tokens.

### Discovery returns no jobs

- Validate the source on the Discovery page.
- Check the provider identifier or board token.
- Review positive OR and negative NOT terms.
- Inspect the latest discovery run for per-source errors.
- Check whether filters or company exclusions rejected the fetched jobs.
- Remember that a source can be healthy while a very narrow query returns zero results.

### Job is visible but cannot be applied to

Open the job detail and inspect rejection reasons. This is expected for current/previous employers, blacklisted companies, and any active hard filter failure. The server intentionally blocks documents, outbound URLs, and application creation for such jobs.

### Completely reset local data

This is destructive and should only be done deliberately:

```bash
docker compose down -v
docker compose up --build
```

The `-v` removes the PostgreSQL volume and all accounts, profiles, jobs, matches, and applications.

## License

No license has been selected yet. All rights are reserved until a license file is added.
