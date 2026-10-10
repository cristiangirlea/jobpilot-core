---
paths:
  - "apps/api/**/*.go"
---
# The Go API

- Every mutating route in `internal/httpapi/server.go` is registered through `w(...)` so it needs
  the write scope; `routes_test.go` reads the router source and fails otherwise.
- Raise `httpapi.Contract` when a change alters what an existing request does, and say in its
  comment what the new number guarantees. The MCP server's `requiredContract` names the version it
  was written against.
- Every private query is scoped by the authenticated `user_id`. A job is global, a match is per user.
- `store.ErrApplicationBlocked` is copied word for word as `blockedMessage` in `apps/mcp`; a test
  pins them, so change both together.
- A new or changed SQL query gets a `...Postgres` test (skips without `JOBPILOT_TEST_DATABASE_URL`).
  Run it against a throwaway container, never the owner's database.
- A model change goes through `internal/models`, the store queries, and `apps/web/lib/types.ts`
  together.
- Test fixtures are invented people and companies, never the owner's profile or CV.
