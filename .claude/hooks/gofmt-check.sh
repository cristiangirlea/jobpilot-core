#!/bin/sh
# After an edit: CI fails on any Go file gofmt would change, in either module. gofmt reads the
# working tree, so CRLF line endings (an old Windows checkout) show up here too.
command -v gofmt >/dev/null 2>&1 || exit 0
cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
files=$(gofmt -l apps/api apps/mcp 2>&1)
if [ -n "$files" ]; then
  printf 'gofmt would change these Go files (CI fails on them); run gofmt -w on each:\n%s\n' "$files" >&2
  exit 2
fi
exit 0
