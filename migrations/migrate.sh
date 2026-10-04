#!/bin/sh
# Applies migrations/*.sql in order, each file once, in one psql session:
#   - an advisory lock first, so runners started together (two API pods' init containers, a
#     compose restart during a deploy) take turns instead of running the same DDL concurrently;
#   - schema_migrations records every applied file, in the same transaction as the file itself,
#     so a file that failed is retried next time and one that succeeded never runs again;
#   - lock_timeout makes a migration that waits on a busy table fail (and be retried) instead of
#     queueing every query behind its lock.
# A database migrated before schema_migrations existed gets every file once more on its first
# run, which is what each start did until then; from then on only new files run.
#
#   DATABASE_URL=postgres://... sh migrations/migrate.sh
#
# MIGRATIONS_DIR defaults to this script's directory; MIGRATE_LOCK_WAIT (default 10min) bounds the
# wait for another runner, MIGRATE_LOCK_TIMEOUT (default 30s) any lock a migration needs.
set -eu

: "${DATABASE_URL:?DATABASE_URL must name the database to migrate}"
dir=${MIGRATIONS_DIR:-$(dirname "$0")}
lock_wait=${MIGRATE_LOCK_WAIT:-10min}
lock_timeout=${MIGRATE_LOCK_TIMEOUT:-30s}

for t in "$lock_wait" "$lock_timeout"; do
  printf '%s\n' "$t" | grep -Eq '^[0-9]+(ms|s|min)$' || { echo "migrate: '$t' is not a duration like 30s or 10min" >&2; exit 2; }
done

# Checked before psql starts, so a bad name cannot leave it running half a script.
files=$(find "$dir" -maxdepth 1 -name '*.sql' | sort)
test -n "$files" || { echo "migrate: no .sql files in $dir" >&2; exit 2; }
for f in $files; do
  basename "$f" | grep -Eq '^[0-9]{3}_[a-z0-9_]+\.sql$' || { echo "migrate: $f is not named NNN_name.sql" >&2; exit 2; }
done

{
  cat <<EOF
SET client_min_messages = warning;
SET lock_timeout = '$lock_wait';
-- Any constant works; this one is "jobpilot" on a phone keypad.
SELECT pg_advisory_lock(56274568) AS locked \gset
SET lock_timeout = '$lock_timeout';
CREATE TABLE IF NOT EXISTS schema_migrations (
    filename text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);
EOF
  for f in $files; do
    name=$(basename "$f")
    cat <<EOF
SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE filename = '$name') AS applied \gset
\if :applied
\echo skip $name
\else
\echo apply $name
BEGIN;
\i '$f'
INSERT INTO schema_migrations (filename) VALUES ('$name');
COMMIT;
\endif
EOF
  done
} | psql "$DATABASE_URL" -X -q -v ON_ERROR_STOP=1
