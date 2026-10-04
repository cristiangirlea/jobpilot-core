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
# Connect directly (CloudNativePG: the <cluster>-rw service), never through a transaction-mode
# pooler: the advisory lock belongs to one server connection, which a pooler hands to others.
# A file must not BEGIN or COMMIT itself (its COMMIT would end the transaction that records it).
#
#   DATABASE_URL=postgres://... sh migrations/migrate.sh
#
# MIGRATIONS_DIR defaults to this script's directory; MIGRATE_LOCK_WAIT (default 10min) bounds the
# wait for another runner, MIGRATE_LOCK_TIMEOUT (default 30s) any lock a migration needs.
set -eu

: "${DATABASE_URL:?DATABASE_URL must name the database to migrate}"
dir=${MIGRATIONS_DIR:-$(dirname "$0")}
cd "$dir"
lock_wait=${MIGRATE_LOCK_WAIT:-10min}
lock_timeout=${MIGRATE_LOCK_TIMEOUT:-30s}

for t in "$lock_wait" "$lock_timeout"; do
  printf '%s\n' "$t" | grep -Eq '^[0-9]+(ms|s|min)$' || { echo "migrate: '$t' is not a duration like 30s or 10min" >&2; exit 2; }
done

# Checked before psql starts, so a bad file cannot leave it running half a script. Names are
# plain (no spaces or quotes), so the lists below split safely and \i needs no quoting.
files=$(find . -maxdepth 1 -name '*.sql' | sed 's|^\./||' | LC_ALL=C sort)
test -n "$files" || { echo "migrate: no .sql files in $dir" >&2; exit 2; }
for f in $files; do
  printf '%s\n' "$f" | grep -Eq '^[0-9]{3}_[a-z0-9_]+\.sql$' || { echo "migrate: $f is not named NNN_name.sql" >&2; exit 2; }
  # A statement on its own line; PL/pgSQL's BEGIN (no semicolon) and END; inside $$ bodies pass.
  ! grep -Eiq '^[[:space:]]*(BEGIN|COMMIT|ROLLBACK|START[[:space:]]+TRANSACTION)[[:space:]]*;' "$f" || { echo "migrate: $f has its own BEGIN/COMMIT; each file already runs in a transaction" >&2; exit 2; }
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
    cat <<EOF
SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE filename = '$f') AS applied \gset
\if :applied
\echo skip $f
\else
\echo apply $f
BEGIN;
\i $f
INSERT INTO schema_migrations (filename) VALUES ('$f');
COMMIT;
\endif
EOF
  done
} | psql "$DATABASE_URL" -X -q -v ON_ERROR_STOP=1
