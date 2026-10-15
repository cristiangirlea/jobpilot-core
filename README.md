# JobPilot Core

JobPilot Core is a self-hosted job discovery, matching, application-writing, and application-tracking MVP. It discovers jobs from supported public APIs, stores each vacancy once in a shared catalog, evaluates that vacancy independently for every user, blocks applications to excluded employers, generates factual application materials without an LLM, and tracks the application lifecycle.

This README is the canonical continuation document for the project. A future developer or Codex session should be able to read this file, inspect only the files named here, and resume work without rediscovering the architecture.

Last verified: **2026-09-22**, by CI on every merge (see Verification).

## Current status

The full local stack is implemented and usable:

- Registration and login with protected, user-isolated data.
- Manual profile, experience, skills, projects, and company exclusions.
- Multiple desired seniority levels.
- Reusable filters with positive (OR) and negative (NOT) title and technology rules.
- Shared job discovery through Remotive, Arbeitnow, Greenhouse, Lever, Ashby, and SmartRecruiters.
- Curated starter employer boards, pasted careers URL detection, source validation, editing, and pause/resume controls.
- Per-user filtering, rejection explanations, warnings, skill gaps, and 0-100 scoring.
- Existing matches are re-evaluated when the main profile, experience, skills, projects, filters, or company exclusions change.
- Deterministic recruiter messages, cover letters, candidate summaries, and common-question answers.
- Application creation, status changes, event history, and outbound-click attribution.
- Strict application lockout for rejected jobs.
- Dashboard metrics and recommended jobs.
- Docker Compose, migrations, seed command, unit tests, and end-to-end smoke scripts.
- An MCP server (`apps/mcp`) that lets agent hosts such as Claude Code read matches, generate documents, and track applications through the same API locks.
- Optional, pluggable AI adapter with a completion cache; disabled by default.
- Reusable answer bank that resolves application questions deterministically first.
- CV upload with deterministic PDF/DOCX parsing, user approval, and profile import.
- CV tailoring per posting, with approve-once-reuse-many for similar roles.
- Reading the employer's published application form (Greenhouse) and resolving it.
- An outbox that composes a whole application and holds it for approval.
- Closed-posting detection: links returning 404/410 and jobs dropped from a full employer board are marked closed, leave job lists and recommendations for everyone, and cannot be composed; an application keeps its closed job as history.
- Curated employer boards and board-token extraction from a pasted careers URL.
- Web pages for CVs, the answer bank, and the outbox.

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

The one submission path is the outbox sender (see "Sending approved applications"): it sends only an application the user approved in the web app, through the employer's own Greenhouse form, over plain HTTP that says what it is, and hands back anything behind a CAPTCHA unsent. Everything else is prepared here and applied to by the user on the employer's page.

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

On first startup, Compose builds the API and web images, waits for PostgreSQL, applies the migrations not applied yet, and starts the services. PostgreSQL data persists in the `jobpilot_postgres` Docker volume.

### First-use workflow

1. Register a user. The Compose stack allows open sign-up; before anyone else can reach the server, name your account in `ADMIN_EMAILS`, set `REGISTRATION=invite` and restart (see "Accounts and sign-up").
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
    internal/answers/                  question classification and answer reuse
    internal/ats/                      reads employers' published application forms
    internal/boards/                   curated employer boards and URL token extraction
    internal/config/config.go          environment configuration
    internal/cv/                       CV text extraction and deterministic parsing
    internal/db/db.go                  PostgreSQL connection
    internal/discovery/fetcher.go      provider adapters
    internal/discovery/service.go      runs, scheduler, dedup, auto-prepare
    internal/generator/generator.go    deterministic application text
    internal/httpapi/server.go         REST router and handlers
    internal/llm/                      optional AI providers, cache, call budget
    internal/matching/matching.go      hard filters and weighted score
    internal/models/models.go          shared domain models
    internal/store/store.go            SQL persistence and isolation
    internal/technology/               the technology vocabulary shared by scoring, answers and CVs
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
migrations/                            idempotent PostgreSQL migrations, migrate.sh and its image
scripts/                               PowerShell smoke and regression tests
docker-compose.yml                     local PostgreSQL, migrations, API, web
deploy/base/                           Kubernetes manifests for the API and web (an overlay adds the rest)
Makefile                               common developer commands
.env.example                           required local secret template
```

## Data ownership and isolation

Jobs are deliberately **not** owned by one user. Two users may match and apply to the same job without duplicating the global vacancy.

### Global catalog data

- `companies`
- `jobs`

A SHA-256 fingerprint deduplicates a job using normalized company, normalized title, location, and the apply URL host/path. Boards repost an opening under a new link, which changes the fingerprint, so a discovered posting with an unknown fingerprint is first compared with the same provider's jobs at that company: the same title and location with a description at least 80% alike, by shared words, is the same opening. Its fingerprint is recorded against the stored job (`job_fingerprint_aliases`), and when the repost is dated later its link, external id and date replace the stored ones. Openings stored once per posting before this existed are tidied once at startup: of each opening's open copies, all but the newest posting are closed with the reason "a later posting of the same opening replaces it", except a copy someone has an application or outbox entry for. `jobs.user_id` is nullable and retained only as a legacy creator/import field.

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
- `notifications`

Every job visible to a user is reached through that user's `job_matches` row. Profiles, filters, match explanations, documents, applications, and history are always scoped by `user_id` in the store layer.

## Implemented pages

| Route | Purpose |
|---|---|
| `/register` | Create an account |
| `/login` | Authenticate |
| `/dashboard` | Completeness, catalog and application metrics, top recommendations, next actions |
| `/notifications` | Everything waiting for the user's decision, with a badge in the navigation; history of what was resolved |
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
| `/cv` | Upload a CV, review the parse, import it, approve it for sending |
| `/answers` | The answer bank: review, edit and approve reusable answers |
| `/outbox` | Composed applications, their blockers and notices, and approval |
| `/settings` | Account and product settings |

Authentication tokens are stored in browser `localStorage`. The API client sends them as bearer tokens; a `401` clears the token and redirects to login.

## Candidate profile

The profile can be filled by hand or imported from an uploaded CV (PDF or DOCX). Parsing is deterministic and never invents a value: a field it cannot find stays empty and earns a warning. It reads sidebar layouts ("Go 5 years"), "Skills: a, b" lines, "Category: a, b (c, d), e / f" skills sections that wrap across lines, pipe-separated headlines and contact lines, and role headers written "Company | Title | Location | Dates" even when the PDF wraps them.

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

- No outbound apply URL. `apply_url` (and `employer_url`) is withheld from every response that carries a rejected match, so a client cannot show an apply link the backend would refuse. `scanJobMatch` and `scanApplication` do this centrally; hiding the button in the UI is not relied upon.
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
| Greenhouse | Employer board token; optional title OR and NOT terms | Public board API; token is the part after `job-boards.greenhouse.io/` or `boards.greenhouse.io/` |
| Lever | Employer site slug; optional title OR and NOT terms | Public postings API for one employer |
| Ashby | Employer board name; optional title OR and NOT terms | Public posting API for one employer |
| SmartRecruiters | Employer/company identifier; optional title OR and NOT terms | Public company postings API; currently capped at 25 plus detail requests |

Employer-direct sources tend to have cleaner provenance and less aggregation churn, and their postings link straight to application forms JobPilot can read, which aggregator pages do not. A board lists every open role, so an employer-direct source can also carry title terms: only titles containing one of its OR terms are taken, and none containing one of its NOT terms. Unlisted jobs are still judged against the whole board. Source validation fetches a small sample before saving so a board token or company identifier can be checked, and counts only the titles the terms keep. The Discovery page also exposes curated starter boards and can extract a supported board identifier from a pasted careers or posting URL.

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
- A run takes at most its configured number of postings (1 to 500, default 100), shared across sources: one posting from each source in turn, so every active source is read on every run. Within a source, postings not yet in the catalog come first, so a small share still reaches new jobs. A source with fewer postings than its share leaves the rest to the others.
- A run counts postings **seen**; jobs **added** to the catalog; how many of those added jobs were **rejected**; and **known** postings (`duplicates_skipped`), which were already in the catalog and are re-scored. Runs recorded before 2026-09-27 also counted re-scored known postings as rejected.
- A process-local mutex permits only one discovery run at a time.
- Source failures are stored on the run; a run fails completely only when every active source fails.
- Global fingerprints deduplicate jobs across users, providers, and repeated runs.
- Every discovered job gets a private match evaluation for the requesting user.

### Closed postings

The scheduler's loop also checks posting links: every 5 minutes it takes up to 25 open jobs that someone still matches, least recently checked first, and checks each at most once a day. A `404` or `410`, or Greenhouse sending a posting back to its board, closes the job for everyone; `403`, `429`, `5xx` and network errors say nothing about the posting and leave it open. An Arbeitnow posting is checked at the employer's page too, once known. Jobs nobody matches are not checked.

A closed job is never deleted. It leaves the job lists the web app shows (`open=true`), the dashboard's recommendations and good-match count, and the MCP `list_jobs` default, and composing an application for it is a `409`. An application for it stays, as the candidate's history, and says the posting closed and why.

A page that says a role is filled but still answers `200` is invisible to the check, so anyone can report a posting as no longer available from its job page (`POST /api/jobs/{id}/report`). The report checks the link at once, and a definite answer closes the job for everyone. Otherwise the job leaves the reporter's lists, recommendations and good-match count, and closes for everyone when a second person reports it: the catalog is shared, and one mistaken report must not hide a live job from others. `DELETE` on the same path takes a report back; a job that closed on reports alone reopens when fewer than two remain, while one its link closed stays closed.

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

The skill match is the share of what a job asks for that the profile holds. What it asks for is every technology from JobPilot's vocabulary (`apps/api/internal/technology`) that the title or description mentions, weighted by how often it is mentioned, plus any skill the active filter lists or the profile holds that the job names. Ways of working such as agile, REST or TDD are not counted. A framework a posting names three times and the profile lacks costs more than a tool it names once. Matched skills keep the profile's own spelling; missing skills are what the job asks for that the profile lacks. A job that names nothing to go on scores a neutral 15.

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

An optional LLM adapter now exists, but deterministic generation remains the default and the fallback. See **Optional AI adapter** and **Answer bank** below. The factuality boundary is unchanged: a provider is only ever given stored candidate facts, and anything it writes is stored unapproved until the user reviews it.

## Optional AI adapter

AI is off by default and the product must keep working with it off. A disabled client returns `ErrUnavailable`, which callers treat as "fall back", never as an error.

Providers:

| Provider | Behaviour |
|---|---|
| `none` | No AI. Every feature uses its deterministic path. |
| `cli` | Runs an installed agent CLI (`claude`, `codex`) as a subprocess. Billed against a subscription rather than per token. |
| `ollama` | A local Ollama server. Private, but output quality depends on local hardware. |

Every completion is cached in `llm_cache`, keyed on provider, model, task, system prompt, and prompt, so an identical request is never paid for twice. Changing the model changes the key, so a switch cannot serve another model's answer. A rolling per-hour call budget bounds runaway usage; cache hits do not consume it.

`GET /api/ai/status` reports what is actually wired up.

## Answer bank

Application questions resolve in a fixed order, and that order is the cost model. Only step four can cost anything.

1. **Stored profile facts** - name, phone, notice period, authorization, salary, links. Pure lookups; no review needed. First and last name come from the profile's given and family names; until those are filled, a split of the full name is offered for review, because which word is the surname cannot be told from the name alone. A salary is stated with its period, "85,000 EUR per year" (yearly, as matching compares it with job salary ranges; a contract rate is per day), and is offered for review, because an employer will hold the candidate to a period the candidate did not write. A notice period of `0` reads "Available immediately"; a bare number, whose unit is unknown, is offered for review.
2. **Exact reuse** - this same question, previously approved for this job, this role family, or every job, the narrowest first.
3. **Similar reuse** - the same canonical question, approved for this job, for a sufficiently similar role and stack, or for every job.
4. **Provider** - only on a genuine miss. Stored unapproved.
5. **Deterministic wording** - the existing generator output.

The reuse keys are all deterministic and computed locally:

- **Canonical question key** from a local pattern table, so "Why are you a good fit?" and "What makes you the right candidate?" resolve to the same stored answer.
- **Role family** from the job title, matched on whole words.
- **Technology signature** from a closed vocabulary, ranked by how often each technology is mentioned and then sorted, so the stored signature is stable and comparable across postings.

Each answer has a **scope**, chosen when it is saved or approved (web, MCP `save_answer` and `approve_answer`): `global` (every job), `role_family` (jobs of its role family, the default, and what every answer was before scopes), or `job` (one job only). A draft remembers the job it was written for, so approving it can narrow it to that job. Resolution prefers the narrowest scope: this job, then this role family, then an answer filed under the `general` family, then every job. A job-only answer is never offered to another job.

Safety rules:

- A generated answer is stored **unapproved** and is never served until the user approves it.
- A question with no supporting fact is left unanswered rather than guessed. Consent and data-policy questions are always left to a human.
- The rejected-job application lock applies here too: resolving answers for a rejected job returns `409`.

## CV upload, parsing, and approval

CV handling is deterministic. CV formats are machine-readable, so no model is involved in reading one, and a field the file does not contain stays empty and earns a warning rather than being invented.

Supported uploads: PDF, DOCX, Markdown, and plain text, up to 12 MB. Format is detected from the file's own bytes, so a mislabelled extension is still read correctly. A scanned image CV is reported as such; there is no OCR.

PDF text needs genuine reconstruction, because a PDF stores glyphs at coordinates rather than words in reading order:

- Columns are separated by locating the blank vertical gutter, so a sidebar does not interleave with the main column.
- Word breaks come from the document's own space runs where it has them. Those runs frequently carry no position, so each is folded into the run it follows; left standing they sort to the front of a line and fuse every word behind them.
- Where a generator emits no spaces at all, breaks are inferred from glyph gaps, with the threshold adapting to whether that file reports real glyph advances.
- A final repair pass fixes only unambiguous over-splits.

Work experience is read from the whole document rather than the experience section alone, because a two-column CV places a page-two sidebar heading in the middle of the role list. A dated line must also carry a title to start a role, which is what keeps education entries from becoming jobs.

The approval gate:

1. Upload. The CV is stored **unapproved**, with the original bytes, the extracted text, and the parse.
2. Review the parse and its warnings.
3. `POST /api/cv/{id}/import` copies reviewed values into the profile. Only non-empty values are written, so a field the parser missed can never blank out something entered by hand. Importing experience is opt-in and reports which employers it just added, because those employers become hard exclusions.
4. `POST /api/cv/{id}/approve` marks the CV as sendable.

The original bytes are kept so the file the user approved is the file that gets sent, never a re-render of it. Re-uploading a byte-identical file updates the existing record and clears approval, since the parse may now differ.

## CV tailoring and reuse

`POST /api/jobs/{id}/cv` decides which CV an application should carry, and returns one of three decisions:

| Decision | Meaning |
|---|---|
| `base` | The approved base CV already covers enough of the posting's technologies |
| `reused` | A tailored CV already approved for this kind of role covers this one too |
| `generated` | A new tailored CV was built. It is stored **unapproved** and cannot be sent until reviewed |

Tailoring re-emphasises what the candidate already has. It never adds and never removes:

- Every role, employer, title and date is carried through unchanged. Dropping an employer for relevance, or letting a gap close, would be a lie told on the candidate's behalf.
- Bullets are **selected and reordered** from the ones already written, never rewritten, so no achievement can drift.
- Skills are reordered so the posting's technologies lead. A technology the posting wants and the candidate lacks is reported as `missing`, never listed as a skill.
- Only the summary is newly worded, and only from stored facts. With no provider configured it falls back to deterministic wording.

Reuse mirrors the answer bank: an approved tailored CV is reused for a later posting in the same role family whose technology signature overlaps by at least 60%. That floor is stricter than the answer bank's, because a CV emphasising the wrong stack undermines a whole application. Regenerating a byte-identical CV reuses the existing approval instead of resetting it.

### Rendering

Tailored CVs render as a single column, and the font is embedded.

Both choices are load-bearing rather than aesthetic. A two-column CV interleaves its sidebar with the main text when a machine reads it - the failure this project's own parser had to be taught around - and the machine reading a submitted CV is usually the employer's ATS. The embedded font exists because the first renderer written here emitted an identity ToUnicode map, which silently turned "București" into a control character for every reader, including an ATS. Any replacement renderer must be verified by reading its own output back; `TestRenderedPDFIsMachineReadable` does exactly that.

## Reading employer application forms

`internal/ats` reads the form an employer's ATS publishes for a job, using read-only public endpoints. Nothing in that package submits anything.

Greenhouse publishes a complete form schema: every question, its type, whether it is required, and the full option list for each select. Reading it up front is what makes automated applying tractable, because each question can be resolved against the answer bank before anything is submitted, and a select is answered with one of its own options rather than by guessing at a page's DOM.

Some employers host their Greenhouse board on their own careers site, so a posting links to `careers.example.com/?gh_jid=123`: the job id, but not the board the API needs. Greenhouse's original board host, asked for that job id alone (`boards.greenhouse.io/embed/job_app?token=123`), answers with a redirect naming the board. The reader makes that one request, reads the redirect without following it, and then reads the form from the board API like any other Greenhouse job. Greenhouse answers 404 for a job id it no longer has, and since the request names the job, that closes it; any other answer leaves the form unread rather than the job closed. The background link check asks the same address instead of the careers page, which looks the same whether the job is open or not. Greenhouse's own embed link carrying only a job token (`boards.greenhouse.io/embed/job_app?token=123`) is looked up the same way.

Ashby has no documented form API, but its public job page loads the form from `jobs.ashbyhq.com/api/non-user-graphql` (`ApiJobPosting`), and the reader asks that endpoint for the same data: sections of fields with a title, a type and a required flag, and the options of each select. It needs no account and returns only what any visitor to the posting sees. Types map onto the same fields as Greenhouse's (a yes/no question becomes a Yes/No select), the resume upload is recognised by Ashby's `_systemfield_resume`, any other upload stays a question for the user, deactivated fields are left out, and a type the reader does not know is kept as text rather than dropped, so a required question is never silently lost. Ashby answers a posting it no longer has with `jobPosting: null`, which closes the job like a 404. Equal-opportunity survey forms are counted as skipped and never answered.

Recruitee serves every careers site, on `<company>.recruitee.com` or on the employer's own domain, with a public JSON API beside its pages: `/api/offers/<slug>` describes an offer and its form. The reader lays it out as the applicant sees it: the standard fields the employer switched on (phone, CV, cover letter, photo, academic title), the location question when it is shown, then the employer's own questions in order, with Recruitee's HTML-escaped text unescaped. A salutation states gender, so an optional one is skipped and a required one is left to the applicant; a photo, like any upload other than the CV and letter, is theirs to add. On recruitee.com a 404 means the offer is gone. A posting on another domain at a `/o/<slug>` path is only read once the API there answers, and a 404 closes it only when that host's offer list shows it is a Recruitee site. An answer that is not Recruitee's JSON means the site is something else, applied to by hand; a timeout or server error leaves the form unread for now, never written off. The API is always asked over https. Those employer domains are reached through a client that refuses private addresses, shared with the link checker, since the host comes from a job link.

Personio publishes no form API for applicants. Its careers pages (`<company>.jobs.personio.de/job/<id>`) are a Next.js site, and the page at `/job/<id>/apply` carries the job's whole form in the data it streams to the browser: each field's label, type, required flag and options, and the document slots. The reader decodes that data, read-only and only on Personio's hosts. Options arrive as an id-to-label object and are read in the page's order; the terse standard labels ("First", "Last") are named the way the answer resolver knows them; the CV and cover-letter slots are attached, and any other document is the applicant's to upload. A 404 for the job means it is gone, and so does being sent to Personio's home page, which is where Personio sends a company it no longer hosts. A page that stops carrying the form leaves it unread, never the job closed.

Lever and SmartRecruiters are detected so the composer can name the ATS, but their forms are not read yet, so those jobs report `ErrUnsupported` and are applied to by hand.

Two kinds of question are never answered automatically, and both are matched on the **label** rather than on where the question sits:

- **Protected characteristics** - gender, race, ethnicity, nationality, disability, veteran status and the like. These are dropped from the form entirely and reported as skipped. Greenhouse has dedicated compliance and demographic blocks, but employers are free to put the same questions in the ordinary list instead, and some do: Canonical asks for gender, nationality and race as regular *required* fields. Trusting the block alone let exactly those questions become answerable, which is why labels are checked.
- **Declarations** - undertakings the applicant makes personally, such as confirming the wording is their own or accepting a policy. These stay on the form but are marked, and the composer leaves them. Ticking one would be making a statement in the user's name, which is a particularly poor thing for a tool that drafts text to do.

### Finding boards

A board token is the company slug in a careers URL, which is a poor thing to have to know. `GET /api/discovery/suggestions` returns employer boards checked against the live API, and `POST /api/discovery/detect` reads the provider and token out of any posting or board link the user pastes. A careers-site link to a Greenhouse posting (`careers.example.com/?gh_jid=123`) names no board, so detection then makes the same single lookup the form reader makes (see above), at Greenhouse only and with a five-second limit; the pasted link is never fetched, and a failed lookup leaves the answer as it was. The Discovery page offers both, and the MCP `detect_board` tool uses detection too.

### Where the deterministic boundary sits

Application questions fall into three kinds, and the split is asserted by tests rather than left to judgement:

| Kind | Example | Handling |
|---|---|---|
| A stored fact answers it | country of residence, previous employer, sponsorship, a location gate | Resolved locally. A provider must never be consulted |
| Only the candidate can answer | high-school results, post-employment restrictions, accessibility needs, how they heard about the role | Left unanswered and flagged. Nothing is invented |
| It needs writing | why this role, describe your experience with X | A provider is legitimate; without one it falls back to deterministic wording, always flagged |

`internal/answers/corpus_test.go` holds 50 question labels read from live Greenhouse boards (Canonical, Grafana Labs, GitLab, Mozilla, Datadog), each tagged with its kind. Three tests enforce the boundary in both directions: stored facts must resolve with no provider configured, personal questions must **not** resolve from stored facts, and writing questions must always ask for review. `TestDeterministicCoverage` is a ratchet - it fails both when coverage drops and when *more* questions resolve than are declared as facts, so neither erosion nor over-reach passes silently.

Measured on GitLab's real 17-field form: 13 fields resolve with no model call, and blockers fall from 10 to 3.

Two rules earn their own note, because the failure modes are not symmetric:

- **Location gates** ("Are you currently located in Bangalore?") resolve Yes for a place the candidate is in, and No for a specific place they are plainly not in. A grouping with no membership test - "the Nordics", "the Balkans" - is left unanswered, because it might contain them. Claiming to live somewhere they do not would be a false statement to an employer; failing to answer only costs a click.
- **Eligibility and sponsorship** resolve only from an explicit authorization statement. A citizenship alone is never enough: an EU citizen still needs sponsorship for a role based in the US, so inferring from it would answer for the wrong jurisdiction.

## The outbox

`POST /api/jobs/{id}/compose` assembles a whole application - the CV, the cover letter, and every form answer - and queues it. **Composition never sends.** The user reviews exactly what an employer would receive. A closed posting is refused with 409; an open job's link is checked again first, because postings close between the background checks.

A background check revisits the links of jobs somebody still matches, at most once a day each, 25 every five minutes with a pause between requests. Only 404, 410 and Greenhouse's `error=true` redirect close a job; timeouts, 403 and 5xx leave it open. The checker refuses loopback, private and link-local addresses (checked after DNS), since the links come from feeds and users. A Greenhouse, Lever or Ashby job that its board's full feed stops listing is closed too, and reopened if the board lists it again.

An Arbeitnow posting links to Arbeitnow's own page, whose `<posting>/apply` redirects to the employer's application page. The check reads that redirect once, without following it, and stores the target as `employer_url` when it is an http or https page off the aggregator. Composing reads the form there, so a posting Arbeitnow found on Greenhouse is answered like any Greenhouse job, and the job page's apply button opens the employer's page directly. The redirect still answers after a posting closes, so it never decides liveness itself; once the employer's page is known, the check visits both, and either one removing the posting closes the job. Remotive has no such redirect and keeps its own link.

An entry records two separate things:

- **Blockers** stop approval: an unapproved tailored CV, a required question with no answer, a missing profile email. Approval is refused while any stands, and that check lives in the store rather than the handler, because it is a safety rule rather than a UI concern.

Approval is the user's consent to send exactly what the entry says, so it is checked again at that moment, not taken from compose time: only an entry waiting for approval (`ready`, or `needs_review` with its blockers cleared) can be approved, and one already approved stays as it is; its CV must still exist and be approved; every required answer a model or the template drafted must have been reviewed (an AI draft approved in the answer bank with the wording the entry carries; template wording never counts, so the user saves their own answer and prepares the entry again); and the posting must still be open. The entry stays locked from the checks to the approval. Only a signed-in browser session can approve: the route refuses agent tokens, whatever their scopes, because the consent cannot come from an agent.
- **Notices** inform without blocking: voluntary demographic questions left blank, or a cover letter that fell back to the template. Treating these as blockers would make an application permanently unapprovable, since there is nothing for the user to fix.

Statuses are `needs_review`, `ready`, `approved`, `sending`, `sent`, `failed`, and `cancelled`. Recomposing an entry the user already approved resets it to await approval again: a prior approval was given for what the entry said then, not for whatever it says now. A sent entry is never overwritten, and one being sent can be neither composed, approved nor cancelled (409).

### Sending approved applications

With `OUTBOX_SENDER_ENABLED=true`, the API sends approved entries through the employer's own form (`internal/sender`). It is off by default, so an instance whose approvals predate sending never sends them on upgrade.

- **Which entries.** Only `approved` ones, oldest first, once `OUTBOX_SEND_DELAY_SECONDS` (default 120) have passed since the approval, so the user can still remove one. An approval wakes the sender for that moment; otherwise it sweeps every minute, ten entries at most, two seconds apart. Taking an entry moves it to `sending` in the same transaction that checks the approval again: the CV still approved, the reviewed answers unchanged, the posting open, and the job not excluded by the user's rules. An entry that fails a check is marked `failed` with the reason.
- **How a form is filled.** Greenhouse only, for now: the board API's form schema is not a way to submit (that needs the employer's key), so the sender reads the job's hosted form page (`job-boards.greenhouse.io/<board>/jobs/<id>`; under a fake `ATS_GREENHOUSE_URL`, that origin) and fills it as an applicant would. Each answer goes into the field the form schema named at compose time, a select gets the option whose label matches, the approved CV is uploaded as the file, the cover letter goes into its box (or as a text file), and the page's hidden values are sent as the page set them. Declarations, personal choices and other uploads are never filled, and neither is an optional answer a model or the template drafted that the user has not approved. A form page over 4 MiB is handed back rather than read in part. The post goes only to the page's own origin, with a `User-Agent` that names JobPilot; nothing pretends to be a browser.
- **What comes back to the user.** Anything the sender may not or cannot fill is never forced, and nothing is posted: a page that loads a CAPTCHA or bot check (reCAPTCHA, hCaptcha, Turnstile and the like; every live Greenhouse and Lever form checked on 2026-10-15 has one), a form built by script rather than plain HTML, a field the entry carries that the form no longer has, a required field it does not carry, an answer the form does not offer as a choice, a page that moved off Greenhouse. The entry is marked `failed` with the reason in `error`, and the web app shows it as a task: open the employer's form, apply, and record it with "I applied". Other ATS forms (Ashby, Recruitee, Personio) are handed back the same way; Lever is not read yet (jobpilot#105).
- **Never twice.** Just before the post, the entry records `submit_attempted_at`, which nothing clears, not even composing it again: an entry posted once is never posted again. A refusal from the form (4xx), or the form answering with itself again, is a hand-back; a server error, no answer, or an answer that confirms nothing (a redirect back to the form or to nowhere, a challenge, a page too large to read) is `failed` with a reason saying it may have arrived. Only a page that is not the form again, or a redirect onward, counts as sent. An entry left `sending` for ten minutes (a stopped API) is written off the same way, saying whether it was posted. A sent entry is `sent` with `sent_at`, and its application moves to `applied` unless it has moved on already.

Measured against a real Greenhouse posting with a 17-field form, 14 fields resolved without a single model call: 10 from stored profile facts, 2 from the answer bank, plus the CV and the cover letter.

## Notifications

One list per user of everything that waits for the user's decision: unapproved answers (`answer`), unapproved CVs (`cv`), and outbox entries in `needs_review` or `ready` (`outbox`). A row opens when an item starts waiting and is resolved, not deleted, once it stops, so the list keeps a history; an item that waits again opens a new row. The web app shows the open count as a badge and the list at `/notifications`; agents read it with `list_pending_decisions` and the `review_pending_decisions` prompt.

Triggers on `answer_bank`, `cv_documents` and `application_outbox` keep these three kinds current in the same transaction as every write, so no code path that changes an approval or an outbox status can leave the list wrong, and each transition is recorded when it happens. `kind` is free text and `subject_id` names the item in its own table, so a new kind needs no schema change: the code that owns the item calls `store.OpenNotification` and `store.ResolveNotification` (the SQL functions `notification_open` and `notification_resolve`) inside its own transaction. Reading the feed changes nothing; pages follow on with `after`, the previous page's `next`. `detail.href` is the web page where an item is resolved; the web app links only to its own paths.

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

Apply buttons use the protected `POST /api/jobs/{id}/outbound` endpoint. It records user, job, and optional application attribution. The endpoint applies at the authorized partner link first, when one is configured; otherwise at the employer's own page (`employer_url`) when known, else the apply URL. It is blocked for rejected jobs. That order is decided in one place, `models.Job.ApplyTarget`. Composing and the MCP server use a different order with no partner link: the employer's page, else the apply URL. Composing reads the form at `models.Job.FormTarget` (a partner link is attribution, never a form), and the MCP server's `applyLinkOrder` sentence tells agents to give the user the `employer_url`, else the `apply_url` (it never sees a partner link).

Click attribution alone does not create revenue. Monetization requires a real employer, affiliate, CPA, recruiting, or hiring-partner agreement plus suitable user disclosure. Do not create or guess third-party referral codes.

## REST API

All request and response bodies are JSON. Protected endpoints require:

```http
Authorization: Bearer <token>
```

### Public endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/health` | Health check; reports the API `contract` version a client may rely on |
| `POST` | `/api/auth/register` | Create user and return token; needs `invite` unless `REGISTRATION=open` |
| `POST` | `/api/auth/login` | Authenticate and return token |
| `GET` | `/api/auth/registration` | The sign-up mode: `invite`, `open` or `closed` |
| `GET` | `/api/auth/providers` | Which other sign-in methods are on (`{"google": true}`) |
| `GET` | `/api/auth/google/start` | Redirect to Google (404 when Google is off) |
| `GET` | `/api/auth/google/callback` | Google's redirect back; ends on the web app's `/login/google?code=...` (with a binding cookie) or `/login?error=...` |
| `POST` | `/api/auth/google/exchange` | Exchange the single-use code from the callback for a token; needs the binding cookie |
| `POST` | `/api/auth/google/link` | Link the Google account from a `?link=1` flow to the signed-in account (browser session only; needs the binding cookie) |

### Invite endpoints

Browser sessions of the accounts in `ADMIN_EMAILS` only; agent tokens are refused.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/invites` | List the invites you created; never returns codes |
| `POST` | `/api/invites` | Create a single-use code (`note`, `expires_in_days` 1-90, default 14) and return it once |
| `DELETE` | `/api/invites/{id}` | Withdraw an unused invite |

### Profile endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET`, `PUT` | `/api/profile` | Read or save the main profile |
| `GET`, `POST` | `/api/experiences` | List or add work experience |
| `DELETE` | `/api/experiences/{id}` | Remove work experience |
| `GET`, `POST` | `/api/skills` | List or add skills |
| `GET` | `/api/skills/suggestions` | Known technologies the profile lacks, ranked by the CV and by how many of the best matches ask for them |
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
| `POST`, `DELETE` | `/api/jobs/{id}/report` | Report a posting as no longer available, or take the report back |
| `GET` | `/api/jobs/{id}` | Job and user-specific match details; `{id}` may also be the job's slug, such as `arbeitnow-remote-python-backend-engineer-berlin-100001` |
| `POST` | `/api/jobs/{id}/generate` | Generate factual application documents |
| `POST` | `/api/jobs/{id}/outbound` | Record attribution and return allowed apply URL |
| `GET` | `/api/matches` | List accepted matches |
| `GET` | `/api/matches/{id}` | Read one match |

Both job lists take optional parameters: `state` (`accepted`, `rejected` or `all`; `/api/matches` is accepted only), `min_score` (0 to 100), `open=true` to leave out closed postings and ones the user reported gone, `q` (up to 60 characters) to keep jobs whose title, description or skills contain it as a whole word, ignoring case (plus and hash belong to a word, so `c` is not `c#`; a final dot is a full stop), `fields=summary` to leave descriptions out, and `limit` (1 to 500) with `offset`. A bad value is a `422` naming the parameter. Without any, the list is whole, as clients built before the parameters expect. Every response carries `X-Total-Count`, the number of jobs matching before `limit` and `offset`. A whole list is large: it was 14.4 MB on a full account, mostly descriptions, so a client that pages should ask for the summary and read one job for its text.

### Discovery endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET`, `PUT` | `/api/discovery/settings` | Read or save scheduler and auto-prepare settings |
| `GET`, `POST` | `/api/discovery/sources` | List or create sources |
| `PUT`, `DELETE` | `/api/discovery/sources/{id}` | Edit, pause/resume, or remove a source |
| `POST` | `/api/discovery/validate` | Test a provider configuration and return sample jobs |
| `GET` | `/api/discovery/suggestions` | Curated starter employer boards |
| `POST` | `/api/discovery/detect` | Extract a source identifier from a supported careers URL |
| `GET` | `/api/discovery/runs` | Run history and provider errors |
| `POST` | `/api/discovery/run` | Run all active sources now |
| `POST` | `/api/discovery/recommended` | Derive a profile-focused search and run active sources |

### AI, answer bank, and CV endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/ai/status` | Whether an AI provider is configured, and which |
| `GET` | `/api/answers` | List the answer bank; `?status=pending` or `approved` |
| `POST` | `/api/answers` | Store a hand-written answer |
| `PATCH` | `/api/answers/{id}` | Edit and/or approve an answer |
| `DELETE` | `/api/answers/{id}` | Remove an answer |
| `POST` | `/api/jobs/{id}/answers` | Resolve application questions for one job |
| `POST` | `/api/cv` | Upload a CV (multipart, field `file`) |
| `GET` | `/api/cv` | List stored CVs |
| `GET` | `/api/cv/{id}` | CV detail, extracted text, and parse |
| `GET` | `/api/cv/{id}/file` | Download the original bytes |
| `POST` | `/api/cv/{id}/approve` | Approve or unapprove a CV for sending |
| `POST` | `/api/cv/{id}/import` | Copy reviewed CV data into the profile |
| `POST` | `/api/jobs/{id}/cv` | Decide which CV this job should carry: base, reused, or newly tailored |
| `POST` | `/api/jobs/{id}/compose` | Compose a full application and queue it for approval; 409 for a closed posting |
| `GET` | `/api/outbox` | The approval queue; `?status=` filters. Each entry carries `cv`, a summary of its CV as it is now (id, filename, kind, approval, and a tailored CV's `alignment` and `missing`), absent once the CV is deleted |
| `GET` | `/api/outbox/{id}` | One composed application in full |
| `POST` | `/api/outbox/{id}/approve` | Release an application for sending; a browser session only (403 for agent tokens), and 409 with the reason while anything holds it back |
| `POST` | `/api/outbox/{id}/cancel` | Drop an application from the queue |
| `GET` | `/api/discovery/suggestions` | Curated employer boards worth following |
| `POST` | `/api/discovery/detect` | Read a board token out of a pasted careers URL |
| `DELETE` | `/api/cv/{id}` | Remove a CV |

### Application and dashboard endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET`, `POST` | `/api/applications` | List or create applications |
| `GET` | `/api/applications/{id}` | Application, documents, and history |
| `PATCH` | `/api/applications/{id}` | Update status and application data |

`PATCH /api/applications/{id}` treats `notes` as optional: omit the field to keep the stored notes, send a string to replace them, and send `""` to clear them. A client that only moves the status therefore cannot erase what the user wrote.
| `GET` | `/api/dashboard` | User-specific metrics and recommendations |
| `GET` | `/api/notifications` | What waits for the user (`state=open`, the default, oldest first, or `all` with the history, newest first; `limit` 1 to 200, default 50; `after`, the previous page's `next`) and `open`, the count of open items |

### Agent token endpoints

These are refused for agent tokens themselves, so a leaked token cannot mint siblings or revoke itself.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/tokens` | List agent tokens; never returns token values |
| `POST` | `/api/tokens` | Mint a token and return it once |
| `DELETE` | `/api/tokens/{id}` | Revoke a token, effective on the next request |

The HTTP layer uses strict JSON decoding with unknown fields rejected and a 2 MiB request-body limit. See `apps/api/internal/httpapi/server.go` for exact payload structs and validation messages.

## MCP server

`apps/mcp` is a Model Context Protocol server that lets an agent host such as Claude Code work with JobPilot through typed tools. It is a client of the REST API and nothing more: every call carries the user's bearer token, so user isolation, the rejection locks, and the no-submission boundary are enforced by the API exactly as they are for the web UI.

It is its own Go module because the MCP SDK requires Go 1.25 and brings dependencies the API does not need. The API module and its Go 1.23 container build are unchanged.

### Build and register

```bash
make mcp
claude mcp add jobpilot -e JOBPILOT_TOKEN=<token> -- /absolute/path/to/apps/mcp/jobpilot-mcp
```

Register the built binary with `claude mcp add`, as above, for day-to-day use. The repository does not ship a project-scoped `.mcp.json`, because a project server overrides a user's server of the same name: a checked-in `jobpilot` entry replaced the registered binary, and its token, in every session opened in the repository, and every tool answered "not configured". To run the server straight from source instead, with `go run` and no build step, copy `.mcp.json.example` to `.mcp.json`, which git ignores, and set `JOBPILOT_TOKEN` in your environment; the host expands it. It runs from whichever checkout the session is in, so a worktree on an older branch runs older code. Either way the server starts without credentials rather than exiting, and every tool answers with what is missing and how to fix it, instead of the host reporting only a closed connection.

To mint an agent token without the web app, run the script below. It asks for your email and password in the terminal, never echoes or stores them, and prints the token once with the ready-made `claude mcp add` command:

```bash
pwsh -File scripts/agent-token.ps1 -Write
```

Drop `-Write` for a read-only token, and pass `-ExpiresInDays 90` for one that lapses. Otherwise, get a token from `POST /api/auth/login`, or let the server log in for you:

| Variable | Purpose |
|---|---|
| `JOBPILOT_API_URL` | API origin, default `http://localhost:8080`; a trailing `/api` is accepted |
| `JOBPILOT_TOKEN` | An agent token created under **Settings > Agent tokens**, or a short-lived login token |
| `JOBPILOT_EMAIL`, `JOBPILOT_PASSWORD` | Fallback when no token is set; the server logs in on first use and once more after a `401`. Prefer an agent token: it is revocable and can be read-only, while a password is not and gives away the account |

The transport is stdio, so the binary logs to stderr only. Each tool call is logged with its name, duration and whether it failed; arguments are never logged, because they carry profile facts and application text. `make mcp` stamps the build with `git describe`, which `--version` prints.

On startup the server reads `GET /api/health`. An unreachable API is a warning, not an exit, since it may simply not be up yet. An API whose `contract` is older than the one the server was written against is also a warning, and the one tool where the difference would lose data, a status-only `update_application_status`, is refused with instructions to pass the notes explicitly.

The catalog an agent host downloads at the start of every session is about 27 KB for 33 tools. Results are typed and returned as structured content, but per-field output schemas are deliberately not advertised: they were two thirds of a 60 KB catalog, and the semantics that mattered now live in the tool descriptions. `TestCatalogStaysWithinBudget` keeps it that way.

### Tools

| Tool | Kind | Purpose |
|---|---|---|
| `get_profile` | read | Candidate facts the agent may use when writing |
| `list_jobs` | read | Accepted (default), rejected, or all evaluated jobs as summaries, with `min_score` and `limit`; one page is fetched from the API. Closed postings only with `include_closed`, marked closed |
| `get_job` | read | Full posting plus score breakdown, reasons, warnings, and rejection reasons |
| `list_pending_decisions` | read | Everything waiting for the user, oldest first, each with `how_to_resolve`; `include_resolved` adds the history, and `after` the following page |
| `report_job_unavailable` | write | Report a posting as no longer available when the user says so; the link is checked at once, and the result says whether it closed for everyone or is hidden from this user until a second report. Taking a report back is web-only |
| `get_filters` | read | Filter presets, so the agent can explain exactly why a job was rejected |
| `set_filters` | write | Create a filter preset or change one by id: only the named rules change, `active` makes it the only active one, and every save re-scores the user's jobs |
| `list_applications` | read | Applications with their job, optionally filtered by status |
| `get_application` | read | One application with its generated documents and blocked state |
| `get_dashboard` | read | Pipeline metrics, top matches, and next actions |
| `list_discovery_runs` | read | Recent run history |
| `list_discovery_sources` | read | Configured sources with their queries and last crawl error |
| `validate_discovery_source` | read | Sample-fetch a provider to check a board token before saving it |
| `generate_documents` | write | Deterministic documents for a job; refused for rejected jobs |
| `create_application` | write | Draft application record; idempotent; refused for rejected jobs |
| `update_application_status` | write | Status change with history; keeps existing notes unless new notes are passed |
| `import_job` | write | Add one posting by hand; a rejected import still returns with its reasons |
| `add_discovery_source` | write | Save a source for the scheduler; validate it first |
| `run_discovery` | write | Run every active source now, reporting progress when the host asks for it |

Apply engine:

| Tool | Kind | Purpose |
|---|---|---|
| `get_ai_status` | read | Whether an AI provider is configured at all |
| `list_answers` | read | The answer bank; pending answers are drafts awaiting approval |
| `list_cvs`, `get_cv` | read | CVs and their extracted text, including what could not be parsed |
| `list_outbox`, `get_outbox_entry` | read | Composed applications, their blockers and every answer's provenance |
| `detect_board` | read | Work out the provider and identifier behind a careers URL |
| `list_board_suggestions` | read | Curated employer boards known to work as sources |
| `resolve_screening_answers` | write | Answer a job's questions; drafts are persisted unapproved |
| `save_answer` | write | Add an answer to the bank, unapproved unless the user confirmed the wording, for every job, a role family or one job |
| `approve_answer` | write | Approve a saved answer, optionally replacing its text and choosing its scope |
| `approve_cv` | write | Approve a CV for reuse |
| `tailor_cv_for_job` | write | Keep, reuse, or write a CV for a posting |
| `compose_application` | write | Assemble a whole application into the outbox for review |
| `cancel_outbox_entry` | write | Withdraw a composed application; reversible |

List tools return summaries without the job description so a page of matches stays small in the agent's context; `get_job` returns the full text. The `apply_url` is blanked for rejected jobs and blocked applications, as the API itself now does, and so is `employer_url`, the employer's own page behind an aggregator's posting, which agents are told to give the user in preference to `apply_url`. Server instructions tell the host that rejected jobs are locked, that nothing here submits or records an apply click, and that generated text may only use stored facts. A `409` carrying the rejection-lock message is returned as a tool error with that hint; other conflicts, such as a duplicate discovery source, are passed through unchanged.

**Deliberately absent, and worth keeping absent:**

- `approve_outbox_entry`. Approving a composed application is consent to send it, and the product's boundary is that the user releases their own applications. Approving an answer or a CV is different: the agent can show the exact text it is asking about, so the user approves something they have read. Approval is what lets the sender send an application, so the gate belongs with the user, and the REST route enforces it too: it refuses agent tokens.
- `POST /api/jobs/{id}/outbound`, because an agent must not fire attribution clicks.
- CV upload, which is multipart and belongs in the web app.
- Registration, deletes, and profile or filter edits.

Server instructions tell the host that composing does not send, that blockers and anything marked `needs_review` must be shown to the user verbatim, and that an application must never be described as approved or sent. Protected-characteristic questions are never answered and declarations the user must make personally are left blank; both are enforced by the API, not by the model.

### Prompts

| Prompt | Argument | Purpose |
|---|---|---|
| `prepare_application` | `job_id` (required) | Walk one job from its match explanation to a tracked application, stopping at a rejection and leaving the submission to the user |
| `weekly_review` | none | Summarise pipeline health, what has stalled, and what to do next; reads the filter to name the rule behind the strongest rejections |
| `review_pending_decisions` | none | Go through what waits for the user one item at a time, show each in the exact words returned, and approve an answer or CV only on the user's word; applications are approved in the web app |

All three encode the rules that matter at the point of use: stop at a rejected job and explain which rule applied, never claim an application was submitted, and write only from stored facts.

### Verification

```bash
cd apps/mcp && go test ./...   # unit tests against a fake API; no stack needed
make smoke-mcp                 # builds the binary and drives it over stdio against http://localhost:8080
```

The end-to-end flow has an optional section that serves a fake Greenhouse form itself and imports a job whose apply URL points at it. Composing prepares a tailored CV, since the test user has none, and holds the entry until that CV is approved; the flow approves it with `approve_cv`, composes again to get an entry with no blockers, approves that entry through the API as the user would in the web app, and proves that rebuilding it withdraws the approval and says so. The same server, told apart by path, fakes an Ashby and a Recruitee board: on each, composing reads the form, answers it from the profile, reports the required question nothing stored answers as a blocker, and refuses a posting the board says is gone. It runs when `JOBPILOT_E2E_ATS_PORT` is set and the API under test has `ATS_GREENHOUSE_URL`, `ATS_ASHBY_URL` and `ATS_RECRUITEE_URL` pointing at that port; CI always does this. Locally, set all three to `http://host.docker.internal:18999` in `.env` before `make dev` and run:

```bash
cd apps/mcp && JOBPILOT_E2E_ATS_PORT=18999 go test -count=1 -run TestEndToEnd . -args -api http://localhost:8080
```

## Agent tokens

A browser login returns a token that expires after `TOKEN_TTL_HOURS` and cannot be revoked on its own. That is the wrong shape for the MCP server or a script, which needs a credential that lasts, can be limited, and can be withdrawn. **Settings > Agent tokens** mints one.

- The value is shown once at creation and never stored; only its SHA-256 digest is, so a copy of the database cannot be replayed against the API.
- Tokens carry the `jpat_` prefix, which makes them recognisable in a config file or a log and lets the middleware route them without a lookup.
- Scopes are `read` (every `GET`, plus `POST /api/discovery/validate` and `POST /api/discovery/detect`, which change nothing) or `read` and `write`. Write implies read.
- Revocation takes effect on the next request. Expiry is optional, from 30 to 365 days, and defaults to never because a revocable token that stops working on a forgotten date is worse than one the user retires deliberately.
- Twenty live tokens per user; `last_used_at` is recorded at most once a minute.
- Agent tokens cannot reach `/api/tokens` at all. Token management requires a browser session.

A browser session holds both scopes and no token id, which is exactly what those two rules key on.

## Accounts and sign-up

JobPilot's own login is the only gate in front of it, so sign-up and login are guarded in the API.

- **Sign-up mode.** `REGISTRATION=invite` (the default) needs a single-use invite code; `open` lets anyone register; `closed` refuses every sign-up. An unknown value stops the API at startup. A new server has no admin and no invites yet: start it with `open`, register the owner's account, name it in `ADMIN_EMAILS`, then restart with `invite`. Every way of creating an account goes through one gate, `checkSignUp` (then `createAccount` for the form) in `internal/httpapi/accounts.go`; Google sign-up asks it without an invite, so it creates accounts only while sign-up is open.
- **Invites.** An account listed in `ADMIN_EMAILS` creates them under **Settings > Invites**, or with `POST /api/invites`. A code (`jpi_` prefix) is shown once, stored only as its SHA-256 digest, lasts 1 to 90 days (14 by default), and is claimed in the same transaction that creates the account, so it works once and a refused sign-up does not spend it. A missing, unknown, used or expired code all get the same 403. Fifty open invites per admin. The register page reads `?invite=` from its address, so a link can carry the code.
- **Rate limits.** Login: 30 attempts per client address and 10 per email in 15 minutes; a successful login clears the email's count. Sign-up: 10 per address and 5 per email an hour. Past a limit the answer is 429 with `Retry-After`, before any database or bcrypt work. Emails are counted by their SHA-256 digest, so the table's size does not depend on what a caller sends. Counts are kept in memory (`internal/ratelimit`), which suits the single API replica; a restart forgets them. The address is the connection's unless `CLIENT_IP_HEADER` names a header the proxy in front overwrites (`CF-Connecting-IP` behind a Cloudflare Tunnel); IPv6 addresses count by their /64.
- **Equal answers.** A login for an unknown email runs a bcrypt comparison against a fixed hash of the same cost and gets the same 401 text as a wrong password, so neither the time nor the text tells whether an account exists.

## Database and migrations

PostgreSQL 16 is used in Compose. Migrations are plain, idempotent SQL. `migrations/migrate.sh` (the `migrate` service, and the `jobpilot-migrate` image built from `migrations/`) applies each file once, in order. It runs in one psql session behind an advisory lock, so runners started together take turns, and it records applied files in `schema_migrations` in the same transaction as the file. A file that fails is rolled back and retried on the next run. `MIGRATE_LOCK_TIMEOUT` (default 30s) bounds how long a migration waits on a busy table, and so how long queries queue behind it, before it fails and is retried. It connects directly to PostgreSQL, never through a transaction-mode pooler, which would hand the advisory lock's connection to other clients. A migration file must not `BEGIN` or `COMMIT` itself; the script rejects one that does. A database migrated before `schema_migrations` existed gets every file once more on its first run, then only new files.

| Migration | Purpose |
|---|---|
| `001_init.sql` | Authentication, profile, companies, jobs, matches, applications, documents, and events |
| `002_seed_demo.sql` | Seed documentation/placeholder; no shared default account |
| `003_discovery.sql` | Global job catalog, discovery settings/sources/runs, fingerprints, outbound attribution |
| `004_boards_and_technology_filters.sql` | Additional board providers and include/exclude technology filters |
| `005_profile_seniorities.sql` | Multiple profile seniorities and legacy migration |
| `006_discovery_exclusions.sql` | Source-level NOT queries and source uniqueness rules |
| `007_llm.sql` | Completion cache for the optional AI adapter |
| `008_answer_bank.sql` | Reusable, approval-gated application answers |
| `009_cv_documents.sql` | Uploaded and generated CVs, extracted text, parse result, approval |
| `010_outbox.sql` | Composed applications held for approval, with blockers and notices |
| `011_agent_tokens.sql` | Revocable, scoped tokens for agents and scripts |
| `012_match_lists_never_null.sql` | Repairs stored match lists saved as JSON null |
| `013_job_slugs.sql` | Readable job names for URLs |
| `014_job_liveness.sql` | Closed postings and the background link check |
| `015_profile_name_parts.sql` | Given and family names for a form's first and last name fields |
| `016_job_reposts.sql` | Fingerprints of reposts, recorded against the job they repost |
| `017_employer_links.sql` | The employer's own application page behind an aggregator's posting |
| `018_job_reports.sql` | Reports that a posting is no longer available, one per person per job |
| `019_answer_scope.sql` | Where an answer is reused: every job, its role family (existing answers), or one job |
| `020_notifications.sql` | The per-user notification feed (what waits for the user, open or resolved), the triggers that keep it current, and a backfill of what already waits |
| `021_outbox_sending.sql` | When an outbox entry was posted to the employer, so it is never sent twice |
| `022_user_identities.sql` | Sign-in identities from Google, found again by the provider's subject |
| `023_invites.sql` | Single-use sign-up invite codes, stored as digests |

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
agent_tokens
user_identities
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
| `REGISTRATION` | `invite` (default), `open` or `closed`; Compose defaults to `open` for local development. See "Accounts and sign-up" |
| `ADMIN_EMAILS` | Comma-separated accounts that may create invites |
| `CLIENT_IP_HEADER` | Header holding the caller's address, set by the proxy in front (`CF-Connecting-IP` behind a Cloudflare Tunnel); empty uses the connection's address |
| `ATS_GREENHOUSE_URL` | Greenhouse board API origin; defaults to the public service. Point it at a fake to exercise composed applications without the network |
| `ATS_ASHBY_URL` | Origin of Ashby's public job pages, whose posting query the form reader calls; defaults to `https://jobs.ashbyhq.com`. Point it at a fake as above |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URL` | Sign-in with Google; off while `GOOGLE_CLIENT_ID` is unset; with it set, the API refuses to start without the other two. The redirect URL is the API's `/api/auth/google/callback`, registered in the Google OAuth client. The web app and the API must be on the same site (the same host, or subdomains of one domain; `localhost` ports count as one site), because the handoff's binding cookie is `SameSite=Lax` |
| `GOOGLE_ALLOW_SIGNUP` | `true` lets a Google account create a JobPilot account when no account has its email and `REGISTRATION=open`; unset, Google signs in only accounts it was linked to under Settings |
| `ATS_RECRUITEE_URL` | One origin for every Recruitee careers site's offer API; unset, each site's own host is used. Point it at a fake as above |
| `OUTBOX_SENDER_ENABLED` | `true` sends approved outbox entries through the employer's form ("Sending approved applications"); anything else, the default, leaves them queued. `GET /api/health` reports it as `sending` |
| `OUTBOX_SEND_DELAY_SECONDS` | How long an approval waits before it is sent, so it can still be removed; defaults to `120` |

### AI environment

All optional. `LLM_PROVIDER=none` is the default and keeps the API fully deterministic.

| Variable | Purpose |
|---|---|
| `LLM_PROVIDER` | `none`, `cli`, or `ollama` |
| `LLM_CLI_COMMAND` | Command for the `cli` provider, e.g. `claude` or `codex` |
| `LLM_CLI_ARGS` | Arguments for it, e.g. `-p --output-format text`. The prompt goes to stdin unless an argument contains `{{prompt}}` |
| `LLM_OLLAMA_URL` | Base URL for the `ollama` provider |
| `LLM_MODEL` | Model name; optional for the CLI provider |
| `LLM_TIMEOUT_SECONDS` | Per-call timeout, default `90` |
| `LLM_CALLS_PER_HOUR` | Rolling call budget, default `120` |

The `cli` provider bills against an existing subscription instead of per token, which makes it the cheapest option. It needs the CLI installed in the same environment as the API, so run the API natively rather than in Compose when using it.

### Frontend environment

| Variable | Compose/default behavior |
|---|---|
| `NEXT_PUBLIC_API_URL` | API base URL; Compose uses `http://localhost:8080` |

No LLM, Redis, Kafka, Elasticsearch, or Kubernetes configuration is required.

## Kubernetes

`deploy/base/` holds the manifests that belong with the code:
- the API Deployment and Service. One replica, replaced with `Recreate`, because discovery's scheduler runs in-process. `jobpilot-migrate` is its init container.
- the web Deployment and Service.

Both run as non-root numeric users with read-only root filesystems. An overlay supplies the rest:
- the namespace;
- a CloudNativePG `Cluster` named `jobpilot-db`, whose `jobpilot-db-app` secret's `uri` becomes `DATABASE_URL`, directly to the primary, never through a pooler;
- a `jobpilot-jwt` secret with key `secret` (required);
- an Ingress sending `/api` to `jobpilot-api` without stripping the prefix, and the rest to `jobpilot-web`;
- the images, pinned by digest.

The lab cluster's overlay lives in the infra repository (`apps/jobpilot`). For that single origin, the web image is built with `NEXT_PUBLIC_API_URL=/api`. CI renders `deploy/base` and validates it with kubeconform.

What the lab runs is pinned in infra's `clusters/local/jobpilot.yaml`: the commit `deploy/base` is read from, and the three images by digest. On `master`, after `publish` pushes the images, CI's `infra bump PR` job (`scripts/infra-bump.sh`) moves one rolling PR in infra, branch `bump/jobpilot`, to that commit and its digests. It skips commits that change nothing deployed (`apps/api`, `apps/web`, `migrations`, `deploy`), never moves the PR back to an older commit, and lists the jobpilot PRs the bump carries. Each run also makes the PR's description match what its branch proposes, whatever an earlier run left half done or someone pushed there, and closes the PR once infra's `main` runs that commit or a newer one. A run whose PR fails to open after its branch was pushed fails; rerunning it, or the next commit that deploys something, opens the PR. Merging that PR is the deploy: Flux applies infra's `main` within a minute. Nothing merges it automatically.

The job authenticates as a GitHub App of its own, and is skipped until the App is set up. To set it up (once, by the owner):

1. Create a GitHub App (Settings, Developer settings, GitHub Apps): no webhook; repository permissions Contents read and write, and Pull requests read and write; installable only on this account.
2. Install it on the `infra` repository only.
3. Generate a private key, then in this repository's Actions settings set the variable `INFRA_BUMP_CLIENT_ID` to the App's client ID and the secret `INFRA_BUMP_APP_KEY` to the key file's contents. Delete the downloaded key afterwards.

The job mints a token for `infra` alone, with only those two permissions, revoked when the job ends (closing its own PR is a pull request write). The script only ever pushes `bump/jobpilot`. Treat the key like a deploy key: anyone who can change this repository's workflows can read it.

## Development commands

```bash
make dev              # build and run PostgreSQL, migrations, API, and web
make test             # Go tests (API and MCP) and frontend TypeScript typecheck
make migrate          # apply SQL migrations not applied yet
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

### Continuous integration

GitHub Actions (`.github/workflows/ci.yml`) runs on every pull request and on pushes to `master`: `gofmt`, `go vet`, and `go test` for `apps/api` and `apps/mcp`, the web typecheck, and an integration job that starts PostgreSQL, applies the migrations, runs the Postgres-backed Go tests (named `...Postgres`, skipped unless `JOBPILOT_TEST_DATABASE_URL` names a migrated database), runs the API, and executes `scripts/smoke.ps1`, `scripts/seniority-filter-smoke.ps1`, and the MCP stdio end-to-end test. The live Remotive smoke (`make smoke-live`) is deliberately not run in CI. Jobs run on the self-hosted runners of the lab Kubernetes cluster (the `infra` repo) only: the repository variable `CI_RUNNER` names its scale set, `lab-jobpilot`, and without it the jobs are skipped rather than sent to GitHub's runners; the workflow installs PowerShell itself where the runner image lacks it. On `master` (pushes, and manual runs, which is how to republish a commit), once every job has passed and only on the lab runners, a `publish` job builds the API, web (with `NEXT_PUBLIC_API_URL=/api`) and migrate images, pushes them to the cluster's registry as `registry.localhost:5000/jobpilot-<name>:<commit>`, and writes the infra overlay's `images:` block, pinned by digest, to the job summary and an `images` artifact.

The test suites cover:

- JWT/password behavior.
- Company normalization, aliases, domains, and fuzzy warnings.
- Matching weights and score bounds.
- Hard filter and blacklist behavior.
- Multiple seniority choices.
- Discovery parsing and query inclusion/exclusion behavior.
- Discovery source editing, pause state, curated suggestions, and board URL detection.
- Deterministic generation without invented profile data.
- Full authentication/profile/filter/job/document/application/outbound API flow.
- Shared catalog behavior across users.
- Re-evaluation after profile, experience, skill, project, filter, and blacklist changes.
- Application action lockout for rejected companies/jobs.
- MCP tool catalog, summaries, filters, blocked-action errors, and notes preservation against a fake API, plus a stdio end-to-end flow against a live API.

CI runs all of the above on every pull request and every merge to master, plus both Docker image builds and the MCP stdio end-to-end test; a green `master` is the verification point.

## Security and reliability notes

Implemented safeguards:

- bcrypt password hashing.
- Optional sign-in with Google through OpenID Connect: the authorization-code flow with PKCE, state and nonce held in a signed, HttpOnly, ten-minute cookie, and an ID token checked for signature, issuer, audience, expiry and a verified email before it is trusted. A Google identity is never joined to an existing account by email, because sign-up does not verify addresses and the account may have been registered by someone else: the account's owner links Google from Settings while signed in. Google creates an account only when `GOOGLE_ALLOW_SIGNUP=true`, sign-up is open (`REGISTRATION=open`; the callback cannot carry an invite, so with invites an invited person registers with the code and then links Google), and no account has its email; an account created that way has no password. The callback hands the web app a single-use code that lasts a minute instead of the token, so a session token never appears in a URL, and the code works only with a binding cookie set in the browser that finished the flow, so a code sent to someone else cannot sign them into the sender's account. The codes are held in memory, which assumes one API instance like the scheduler lock.
- Signed, expiring JWT bearer tokens.
- Protected routes and user-scoped SQL access.
- Strict JSON parsing and request size limits.
- Exact configured CORS origin.
- Bounded provider requests and per-run limits.
- PostgreSQL transactions and constraints for core state changes.
- Backend-enforced rejection/apply locks.
- Graceful API shutdown and startup database retries.

Local-MVP limitations:

- Browser tokens are stored in `localStorage`, not secure HTTP-only cookies, and cannot be revoked individually; rotating `JWT_SECRET` invalidates all of them. Agent tokens are revocable one by one and can be limited to read access.
- Local Compose uses HTTP and a development database password.
- There is no email verification, password reset, MFA, or rate limiter.
- The scheduler lock is process-local and is intended for one API instance.
- Discovery APIs can change or rate-limit without notice.
- This deployment is not hardened for an internet-facing production environment.

Before public production use, add secure cookies or another hardened token strategy, TLS, secret management, rate limiting, account recovery, audit/observability, backups, provider terms review, privacy controls, and a cross-instance scheduler lock.

## Known limitations and best next tasks

These are known gaps, not hidden surprises:

1. **Provider pagination:** Arbeitnow currently uses the first page, SmartRecruiters is capped at 25, and Remotive at 100. Add bounded pagination only with clear per-run limits and cancellation.
2. **Source management depth:** sources can be added, edited, validated, paused/resumed, and deleted. Per-source schedules and richer per-provider diagnostics can still be improved.
3. **Catalog scale:** the job lists search, filter and page on the server, and both the web job list (50 summaries at a time, with Load more) and the MCP server use that.
4. **Automated submission:** the sender sends approved entries through Greenhouse's hosted form when `OUTBOX_SENDER_ENABLED` is on, but every live Greenhouse and Lever form checked carries a CAPTCHA, so real entries mostly come back as one-click tasks. Lever forms are not read yet (jobpilot#105). Nothing may bypass CAPTCHA or automate job-board portals.
5. **Aggregator coverage:** measured on live feeds, only 1 of 18 Remotive postings and 0 of 175 Arbeitnow postings expose an apply email, and both keep the applicant on their own domain rather than reaching the employer. Employer-direct ATS boards are the viable path; email apply is a small supplementary slice.
6. **Referral operations:** outbound attribution exists, but there is no partner administration, agreement workflow, payout ledger, or disclosure management UI.
7. **Production auth and operations:** see the security section above.
8. **Official application APIs:** external submission is intentionally absent. Add only authorized ATS/employer integrations, never generic stealth automation.

Suggested priority order:

1. Add integration tests around store/HTTP authorization and rejected-job locks.
2. Improve provider diagnostics and add per-source schedules.
3. Add bounded pagination plus job archival/expiration.
4. Harden authentication and operations for deployment.
5. Add partner administration only after a real commercial agreement exists.

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
- The application must continue to work with no LLM key; every AI path needs a deterministic fallback.
- A generated answer or CV is never reused or sent before the user approves it.
- Composition never sends. Only an explicit approval may release an application.
- Protected-characteristic and demographic questions are never auto-answered.
- Tailoring may reorder and select, never add, rewrite, or drop a role.
- Simple facts (name, contact, city, country, employer, eligibility) are derived once, deterministically, and reused; a model is never asked to re-derive them.
- A question only the candidate can answer is left unanswered, never guessed from stored facts.
- A CV parser must leave a field it cannot read empty and warn, never guess.
- Provider fetches are explicit, bounded, cancellable, and terms-conscious.
- Do not add CAPTCHA bypass, stealth submission, LinkedIn/Indeed automation, Kafka, Kubernetes, or Elasticsearch to this MVP.
- Keep PostgreSQL as the source of truth; introduce another service only for a demonstrated need.
- The MCP server is a client of the REST API: it never reads the database, never bypasses the API's authorization, and never exposes outbound attribution or any submission action.
- Every mutating route is registered with the `w(` wrapper so it requires the write scope; `apps/api/internal/httpapi/routes_test.go` reads the router and fails otherwise. Only `validate` and `detect` are reachable with `read`, because they change nothing.
- Every way of creating an account goes through `checkSignUp` and `createAccount`, so the registration mode and invites apply to all of them. A login for an unknown email does the same bcrypt work and gets the same answer as a wrong password.
- Raise `httpapi.Contract` when a change alters what an existing request does, and say what the new number guarantees in its comment. The MCP server's `requiredContract` names the version it was written against.
- Adding an MCP tool means three edits: the registration, the expected-name set in `apps/mcp/e2e_test.go`, and the tools table in this README. Register it through `addTool`, never `mcp.AddTool` directly, so the output-schema policy applies.
- The MCP server's `blockedMessage` copies `store.ErrApplicationBlocked` word for word because the modules cannot import each other; a test pins them and both must change together.

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

Only files not yet recorded in `schema_migrations` run. A normal restart should not require deleting data. To run a file again, delete its row from `schema_migrations` first.

### Make says `No rule to make target`

Some Windows GNU Make builds may fail to auto-discover `Makefile` even when it is present. Use the explicit form:

```bash
make -f Makefile test
make -f Makefile smoke-regression
```

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
