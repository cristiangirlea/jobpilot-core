# JobPilot Core

JobPilot Core is a self-hosted job discovery, matching, application-writing, and application-tracking MVP. It discovers jobs from supported public APIs, stores each vacancy once in a shared catalog, evaluates that vacancy independently for every user, blocks applications to excluded employers, generates factual application materials without an LLM, and tracks the application lifecycle.

This README is the canonical continuation document for the project. A future developer or Codex session should be able to read this file, inspect only the files named here, and resume work without rediscovering the architecture.

Last verified: **2026-08-28**.

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
| `/cv` | Upload a CV, review the parse, import it, approve it for sending |
| `/answers` | The answer bank: review, edit and approve reusable answers |
| `/outbox` | Composed applications, their blockers and notices, and approval |
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

- No outbound apply URL. `apply_url` is withheld from every response that carries a rejected match, so a client cannot show an apply link the backend would refuse. `scanJobMatch` and `scanApplication` do this centrally; hiding the button in the UI is not relied upon.
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

Employer-direct sources tend to have cleaner provenance and less aggregation churn. Source validation fetches a small sample before saving so a board token or company identifier can be checked. The Discovery page also exposes curated starter boards and can extract a supported board identifier from a pasted careers or posting URL.

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

1. **Stored profile facts** - name, phone, notice period, authorization, salary, links. Pure lookups; no review needed.
2. **Exact reuse** - this same question, previously approved for this role family.
3. **Similar reuse** - the same canonical question, approved for a sufficiently similar role and stack.
4. **Provider** - only on a genuine miss. Stored unapproved.
5. **Deterministic wording** - the existing generator output.

The reuse keys are all deterministic and computed locally:

- **Canonical question key** from a local pattern table, so "Why are you a good fit?" and "What makes you the right candidate?" resolve to the same stored answer.
- **Role family** from the job title, matched on whole words.
- **Technology signature** from a closed vocabulary, ranked by how often each technology is mentioned and then sorted, so the stored signature is stable and comparable across postings.

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

Lever, Ashby and SmartRecruiters are detected so the composer can name the ATS, but they do not publish their schemas, so those jobs report `ErrUnsupported` and are applied to by hand.

Two kinds of question are never answered automatically, and both are matched on the **label** rather than on where the question sits:

- **Protected characteristics** - gender, race, ethnicity, nationality, disability, veteran status and the like. These are dropped from the form entirely and reported as skipped. Greenhouse has dedicated compliance and demographic blocks, but employers are free to put the same questions in the ordinary list instead, and some do: Canonical asks for gender, nationality and race as regular *required* fields. Trusting the block alone let exactly those questions become answerable, which is why labels are checked.
- **Declarations** - undertakings the applicant makes personally, such as confirming the wording is their own or accepting a policy. These stay on the form but are marked, and the composer leaves them. Ticking one would be making a statement in the user's name, which is a particularly poor thing for a tool that drafts text to do.

### Finding boards

A board token is the company slug in a careers URL, which is a poor thing to have to know. `GET /api/discovery/suggestions` returns employer boards checked against the live API, and `POST /api/discovery/detect` reads the provider and token out of any posting or board link the user pastes. The Discovery page offers both.

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

`POST /api/jobs/{id}/compose` assembles a whole application - the CV, the cover letter, and every form answer - and queues it. **Composition never sends.** The user reviews exactly what an employer would receive.

An entry records two separate things:

- **Blockers** stop approval: an unapproved tailored CV, a required question with no answer, a missing profile email. Approval is refused while any stands, and that check lives in the store rather than the handler, because it is a safety rule rather than a UI concern.
- **Notices** inform without blocking: voluntary demographic questions left blank, or a cover letter that fell back to the template. Treating these as blockers would make an application permanently unapprovable, since there is nothing for the user to fix.

Statuses are `needs_review`, `ready`, `approved`, `sent`, `failed`, and `cancelled`. Recomposing an entry the user already approved resets it to await approval again: a prior approval was given for what the entry said then, not for whatever it says now. A sent entry is never overwritten.

Measured against a real Greenhouse posting with a 17-field form, 14 fields resolved without a single model call: 10 from stored profile facts, 2 from the answer bank, plus the CV and the cover letter.

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
| `POST` | `/api/jobs/{id}/compose` | Compose a full application and queue it for approval |
| `GET` | `/api/outbox` | The approval queue; `?status=` filters |
| `GET` | `/api/outbox/{id}` | One composed application in full |
| `POST` | `/api/outbox/{id}/approve` | Release an application for sending |
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

The HTTP layer uses strict JSON decoding with unknown fields rejected and a 2 MiB request-body limit. See `apps/api/internal/httpapi/server.go` for exact payload structs and validation messages.

## MCP server

`apps/mcp` is a Model Context Protocol server that lets an agent host such as Claude Code work with JobPilot through typed tools. It is a client of the REST API and nothing more: every call carries the user's bearer token, so user isolation, the rejection locks, and the no-submission boundary are enforced by the API exactly as they are for the web UI.

It is its own Go module because the MCP SDK requires Go 1.25 and brings dependencies the API does not need. The API module and its Go 1.23 container build are unchanged.

### Build and register

```bash
make mcp
claude mcp add jobpilot -e JOBPILOT_TOKEN=<token> -- /absolute/path/to/apps/mcp/jobpilot-mcp
```

The repository also ships a project-scoped `.mcp.json` that runs the server straight from source with `go run`, so a clone needs no build step and no absolute path, only Go 1.25 on `PATH`. It carries no secret: set `JOBPILOT_TOKEN` in your environment and the host expands it. Claude Code asks once per project before trusting a checked-in server.

Get a token from `POST /api/auth/login`, or let the server log in for you:

| Variable | Purpose |
|---|---|
| `JOBPILOT_API_URL` | API origin, default `http://localhost:8080`; a trailing `/api` is accepted |
| `JOBPILOT_TOKEN` | Bearer token from login or registration |
| `JOBPILOT_EMAIL`, `JOBPILOT_PASSWORD` | Alternative to a token; the server logs in on first use and once more after a `401`, which covers token expiry. Prefer a token: values passed with `claude mcp add -e` are stored in plaintext in the host's config, and a password never expires |

The transport is stdio, so the binary logs to stderr only. Each tool call is logged with its name, duration and whether it failed; arguments are never logged, because they carry profile facts and application text. `make mcp` stamps the build with `git describe`, which `--version` prints.

### Tools

| Tool | Kind | Purpose |
|---|---|---|
| `get_profile` | read | Candidate facts the agent may use when writing |
| `list_jobs` | read | Accepted (default), rejected, or all evaluated jobs as summaries, with `min_score` and `limit` |
| `get_job` | read | Full posting plus score breakdown, reasons, warnings, and rejection reasons |
| `get_filters` | read | Filter presets, so the agent can explain exactly why a job was rejected |
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

List tools return summaries without the job description so a page of matches stays small in the agent's context; `get_job` returns the full text. The `apply_url` is blanked for rejected jobs and blocked applications, as the API itself now does. Server instructions tell the host that rejected jobs are locked, that nothing here submits or records an apply click, and that generated text may only use stored facts. A `409` carrying the rejection-lock message is returned as a tool error with that hint; other conflicts, such as a duplicate discovery source, are passed through unchanged.

Deliberately absent: `POST /api/jobs/{id}/outbound` (an agent must not fire attribution clicks), registration, deletes, and profile or filter edits.

### Prompts

| Prompt | Argument | Purpose |
|---|---|---|
| `prepare_application` | `job_id` (required) | Walk one job from its match explanation to a tracked application, stopping at a rejection and leaving the submission to the user |
| `weekly_review` | none | Summarise pipeline health, what has stalled, and what to do next |

Both encode the rules that matter at the point of use: stop at a rejected job and explain which rule applied, never claim an application was submitted, and write only from stored facts.

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
| `007_llm.sql` | Completion cache for the optional AI adapter |
| `008_answer_bank.sql` | Reusable, approval-gated application answers |
| `009_cv_documents.sql` | Uploaded and generated CVs, extracted text, parse result, approval |
| `010_outbox.sql` | Composed applications held for approval, with blockers and notices |

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

### Continuous integration

GitHub Actions (`.github/workflows/ci.yml`) runs on every pull request and on pushes to `master`: `gofmt`, `go vet`, and `go test` for `apps/api` and `apps/mcp`, the web typecheck, and an integration job that starts PostgreSQL, applies the migrations, runs the API, and executes `scripts/smoke.ps1`, `scripts/seniority-filter-smoke.ps1`, and the MCP stdio end-to-end test. The live Remotive smoke (`make smoke-live`) is deliberately not run in CI.

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

1. **Provider pagination:** Arbeitnow currently uses the first page, SmartRecruiters is capped at 25, and Remotive at 100. Add bounded pagination only with clear per-run limits and cancellation.
2. **Source management depth:** sources can be added, edited, validated, paused/resumed, and deleted. Per-source schedules and richer per-provider diagnostics can still be improved.
3. **Catalog scale:** job and match lists need stronger pagination/search/index review before very large catalogs.
4. **Automated submission:** still not implemented. Applications are composed and queued; nothing sends them. The next step is a sender that picks up `approved` entries. Nothing may bypass CAPTCHA or automate job-board portals.
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
