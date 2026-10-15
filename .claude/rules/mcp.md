---
paths:
  - "apps/mcp/**"
---
# The MCP server

- It is a client of the REST API: it never reads the database, never bypasses the API's
  authorization, and never exposes outbound attribution or any submission action.
- Register a tool through `addTool`, never `mcp.AddTool` directly; `TestCatalogStaysWithinBudget`
  keeps the catalog small (output schemas are deliberately not advertised).
- A new tool is three edits: the registration, the expected-name set in `e2e_test.go`, and the
  tools table in `README.md`.
- No `approve_outbox`, `send*`, `upload*` or `delete*` tool, ever: approving an outbox entry is
  consent to send and stays in the web app. Approving an answer or a CV is allowed.
- `upload_cv` is the single named exception (owner, 2026-10-16): it uploads to the user's own
  JobPilot, the CV stays unapproved, and nothing reaches an employer. Keep its guard in
  `cvupload.go` whole: off unless `JOBPILOT_CV_DIR` is set, `.pdf`/`.docx` only, at most the API's
  12 MB, opened through `os.Root` (no `..`, symlink or junction out of the folder), first bytes
  matching the extension. Never widen it to other formats, folders or a second upload tool.
- Tool descriptions tell the host to show `needs_review`, unapproved items and blockers in the
  exact words returned, and to use only facts from the profile, CV and job. Keep that wording.
- Logs go to stderr only (stdio transport); never log tool arguments, they carry profile facts.
