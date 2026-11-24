---
paths:
  - "apps/api/**/*.go"
---
# The Go API

- Every mutating route in `internal/httpapi/server.go` is registered through `w(...)` so it needs
  the write scope, except the ones no agent token may reach: `session(...)` for token management,
  `consent(...)` for approving an application, `repair(...)` for deciding a profile repair, and
  `admin(...)` for invites (`ADMIN_EMAILS` only).
  Discovery and the AI status are for admins (#128) through `ap(...)` (read) and `aw(...)` (write),
  which also accept an admin's agent token; `adminOnly` in `routes_test.go` lists them.
  `routes_test.go` reads the router source and fails otherwise.
- A path that calls the AI provider takes its client from `s.llmFor(ctx)`, never `s.LLM`, so
  members (#128) get the deterministic drafts.
- Raise `httpapi.Contract` when a change alters what an existing request does, and say in its
  comment what the new number guarantees. The MCP server's `requiredContract` names the version it
  was written against.
- Every private query is scoped by the authenticated `user_id`. A job is global, a match is per user.
- `store.ErrApplicationBlocked` is copied word for word as `blockedMessage` in `apps/mcp`; a test
  pins them, so change both together.
- A new or changed SQL query gets a `...Postgres` test (skips without `JOBPILOT_TEST_DATABASE_URL`).
  Run it against a throwaway container, never the owner's database.
- A model change goes through `internal/models`, the store queries, and, for a model the web app
  reads, `apps/web/lib/types.ts` together.
- Test fixtures are invented people and companies, never the owner's profile or CV.
