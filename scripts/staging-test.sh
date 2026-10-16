#!/bin/sh
# Tests scripts/staging.sh without starting anything: how REF is resolved (origin branch, pr/N,
# commit) against a local bare repository standing in for GitHub, what it refuses, the shared state
# across worktrees, and the exact commands a dry run prints. Where Docker is available it also
# renders the staging Compose config with a hostile environment and checks the override still wins
# (CI sets STAGING_TEST_REQUIRE_COMPOSE so that part cannot be skipped). It never reads a .env.
# Run with sh from anywhere (CI uses dash); needs git.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0

pass() { echo "ok   $1"; }
fail() { echo "FAIL $1"; failures=$((failures + 1)); }
has() { # DESCRIPTION FILE TEXT (on failure says what is missing, never prints the file)
  if grep -qF -- "$3" "$2"; then pass "$1"; else fail "$1: no [$3]"; fi
}
lacks() { # DESCRIPTION FILE TEXT
  if grep -qF -- "$3" "$2"; then fail "$1: found [$3]"; else pass "$1"; fi
}
refuses() { # DESCRIPTION WANT-IN-MESSAGE COMMAND...
  d=$1 want=$2
  shift 2
  if "$@" > "$work/out" 2>&1; then
    fail "$d: it succeeded"
  elif grep -qF -- "$want" "$work/out"; then
    pass "$d"
  else
    fail "$d: message [$(head -c 300 "$work/out")] lacks [$want]"
  fi
}

# A clone whose origin is a local bare repository: master, a branch only on origin, a branch that
# is ahead on origin of the local copy, and a pull request head the way GitHub publishes it.
git init -q --bare "$work/origin.git"
git clone -q "$work/origin.git" "$work/clone" 2> /dev/null
cd "$work/clone"
git config user.email test@example.com
git config user.name test
mkdir -p scripts deploy/staging
cp "$here/staging.sh" scripts/
cp "$repo/deploy/staging/compose.override.yml" deploy/staging/
echo 'services: {}' > docker-compose.yml
git add -A
git commit -q -m one
git branch -M master
git push -q origin master
master=$(git rev-parse HEAD)
git commit -q --allow-empty -m two
feature=$(git rev-parse HEAD)
git push -q origin HEAD:refs/heads/feature
git commit -q --allow-empty -m three
pr=$(git rev-parse HEAD)
git push -q origin HEAD:refs/pull/7/head
git branch -q stale "$master"
git commit -q --allow-empty -m four
stale_origin=$(git rev-parse HEAD)
git push -q origin HEAD:refs/heads/stale
git reset -q --hard "$master"

stage() { STAGING_DRY_RUN=1 sh scripts/staging.sh "$@"; }
root=$(git rev-parse --show-toplevel)
st="$(git rev-parse --path-format=absolute --git-common-dir)/jobpilot-staging"
clean="+ env -i STAGING_WEB_PORT=13000 STAGING_API_PORT=18081 <docker's own variables>"
compose="$clean docker compose -p jobpilot-staging --project-directory $st/src -f $st/src/docker-compose.yml -f $root/deploy/staging/compose.override.yml"

env OUTBOX_SENDER_ENABLED=true SMTP_HOST=smtp.example.com STAGING_DRY_RUN=1 sh scripts/staging.sh up master > "$work/master.out" 2>&1 || fail "up master: $(head -c 300 "$work/master.out")"
has "master: checks out origin/master" "$work/master.out" "+ git -C $root worktree add -q --detach $st/src $master"
has "master: builds and starts the staging project in a stripped environment" "$work/master.out" "$compose up -d --build --wait"
has "master: seeds the sample profile" "$work/master.out" "$compose run --rm --no-deps --entrypoint /app/seed api"
has "master: creates the staging user, login kept in a file" "$work/master.out" "+ create staging@jobpilot.test through POST /api/auth/register, login in $st/login.txt"
lacks "master: the shell's outbox setting does not reach compose" "$work/master.out" "OUTBOX_SENDER_ENABLED"
lacks "master: the shell's SMTP host does not reach compose" "$work/master.out" "smtp.example.com"

stage up feature > "$work/feature.out" 2>&1 || fail "up feature: $(head -c 300 "$work/feature.out")"
has "a branch only on origin resolves to origin's commit" "$work/feature.out" "--detach $st/src $feature"
stage up stale > "$work/stale.out" 2>&1 || fail "up stale: $(head -c 300 "$work/stale.out")"
has "origin's branch wins over a stale local one" "$work/stale.out" "--detach $st/src $stale_origin"
stage up pr/7 > "$work/pr.out" 2>&1 || fail "up pr/7: $(head -c 300 "$work/pr.out")"
has "pr/7 resolves to the pull request's head" "$work/pr.out" "--detach $st/src $pr"
stage up "$feature" > "$work/sha.out" 2>&1 || fail "up <sha>: $(head -c 300 "$work/sha.out")"
has "a commit resolves to itself" "$work/sha.out" "--detach $st/src $feature"
stage up "$(echo "$feature" | cut -c1-10)" > "$work/short.out" 2>&1 || fail "up <short sha>: $(head -c 300 "$work/short.out")"
has "a short commit resolves" "$work/short.out" "--detach $st/src $feature"

refuses "an unknown ref" "unknown branch" stage up no-such-branch
refuses "pr/ without a number" "pull request number" stage up pr/abc
refuses "a pull request that does not exist" "cannot fetch pull request 99" stage up pr/99
refuses "no REF" "usage: make staging-up" stage up
refuses "the web on 3000" "owner's stack" env STAGING_WEB_PORT=3000 STAGING_DRY_RUN=1 sh scripts/staging.sh up master
refuses "the API on 8080" "owner's stack" env STAGING_API_PORT=8080 STAGING_DRY_RUN=1 sh scripts/staging.sh up master
refuses "the API on 5432" "owner's stack" env STAGING_API_PORT=5432 STAGING_DRY_RUN=1 sh scripts/staging.sh up master
refuses "8080 with a leading zero" "without leading zeros" env STAGING_API_PORT=08080 STAGING_DRY_RUN=1 sh scripts/staging.sh up master
refuses "a port above 65535" "from 1 to 65535" env STAGING_WEB_PORT=70000 STAGING_DRY_RUN=1 sh scripts/staging.sh up master
refuses "a port that is not a number" "port number" env STAGING_WEB_PORT=web STAGING_DRY_RUN=1 sh scripts/staging.sh up master
refuses "the same port twice" "must differ" env STAGING_WEB_PORT=14000 STAGING_API_PORT=14000 STAGING_DRY_RUN=1 sh scripts/staging.sh up master

# One staging per clone: a second worktree sees what the first one started, and down from there
# takes down that checkout with its own compose file.
mkdir -p "$st/src"
echo 'services: {}' > "$st/src/docker-compose.yml"
echo "master $master" > "$st/ref"
git worktree add -q --detach "$work/wt2" master
(cd "$work/wt2" && STAGING_DRY_RUN=1 sh scripts/staging.sh status) > "$work/wt2-status.out" 2>&1 || true
has "another worktree's status sees the staging that is up" "$work/wt2-status.out" "staging is up: master $master"
refuses "another worktree cannot start a second staging" "already up" sh -c "cd '$work/wt2' && STAGING_DRY_RUN=1 sh scripts/staging.sh up master"
(cd "$work/wt2" && STAGING_DRY_RUN=1 sh scripts/staging.sh down) > "$work/down-src.out" 2>&1 || fail "down with a checkout: $(head -c 300 "$work/down-src.out")"
wt2compose="$clean docker compose -p jobpilot-staging --project-directory $st/src -f $st/src/docker-compose.yml -f $(git -C "$work/wt2" rev-parse --show-toplevel)/deploy/staging/compose.override.yml"
has "down with a checkout uses the staged compose file" "$work/down-src.out" "$wt2compose down -v --rmi local --remove-orphans"
has "down removes the staged checkout" "$work/down-src.out" "worktree remove --force $st/src"
git worktree remove --force "$work/wt2"
rm -rf "$st"

stage down > "$work/down.out" 2>&1 || fail "down: $(head -c 300 "$work/down.out")"
has "down without a checkout names only the staging project" "$work/down.out" "$clean docker compose -p jobpilot-staging down -v --rmi local --remove-orphans"
has "down prunes the worktrees" "$work/down.out" "+ git -C $root worktree prune"
if [ "$(grep -c 'docker compose' "$work/down.out")" = 1 ]; then pass "down runs one compose command"; else fail "down runs $(grep -c 'docker compose' "$work/down.out") compose commands"; fi
[ ! -e "$st" ] && pass "a dry run creates nothing" || fail "a dry run created $st"

# The override itself: loopback ports only, never the owner's, and the outside-facing settings off.
override="$repo/deploy/staging/compose.override.yml"
has "the API is published on loopback" "$override" '"127.0.0.1:${STAGING_API_PORT:-18081}:8080"'
has "the web is published on loopback" "$override" '"127.0.0.1:${STAGING_WEB_PORT:-13000}:3000"'
has "Postgres is not published" "$override" "ports: !reset []"
has "outbox sending is off" "$override" 'OUTBOX_SENDER_ENABLED: "false"'
has "no AI provider" "$override" "LLM_PROVIDER: none"
has "no email" "$override" 'SMTP_HOST: ""'
has "no Google sign-in" "$override" 'GOOGLE_CLIENT_ID: ""'

# Rendered with the real docker-compose.yml, an empty env file instead of any .env, and a hostile
# environment: the override still wins.
if docker compose version > /dev/null 2>&1; then
  mkdir "$work/empty"
  : > "$work/empty/.env"
  if env OUTBOX_SENDER_ENABLED=true SMTP_HOST=smtp.example.com LLM_PROVIDER=cli GOOGLE_CLIENT_ID=hostile-google \
    REGISTRATION=closed JWT_SECRET=hostile-secret ADMIN_EMAILS=hostile@example.com \
    docker compose -p jobpilot-staging-test --project-directory "$work/empty" --env-file "$work/empty/.env" \
    -f "$repo/docker-compose.yml" -f "$override" config > "$work/config.yml" 2> "$work/config.err"; then
    has "rendered: outbox sending stays off" "$work/config.yml" 'OUTBOX_SENDER_ENABLED: "false"'
    has "rendered: no AI provider" "$work/config.yml" "LLM_PROVIDER: none"
    has "rendered: sign-up is open" "$work/config.yml" "REGISTRATION: open"
    has "rendered: the staging JWT secret" "$work/config.yml" "JWT_SECRET: staging-only-not-a-secret"
    has "rendered: only the staging admin" "$work/config.yml" "ADMIN_EMAILS: staging@jobpilot.test"
    lacks "rendered: the shell's SMTP host is ignored" "$work/config.yml" "smtp.example.com"
    lacks "rendered: the shell's Google client is ignored" "$work/config.yml" "hostile-google"
    lacks "rendered: the shell's JWT secret is ignored" "$work/config.yml" "hostile-secret"
    lacks "rendered: the shell's admin is ignored" "$work/config.yml" "hostile@example.com"
    published=$(grep -c "published:" "$work/config.yml" || true)
    loopback=$(grep -c "host_ip: 127.0.0.1" "$work/config.yml" || true)
    if [ "$published" = 2 ] && [ "$loopback" = 2 ]; then pass "rendered: two ports, both on loopback"; else fail "rendered: $published published ports, $loopback on 127.0.0.1"; fi
    has "rendered: the API on 18081" "$work/config.yml" 'published: "18081"'
    has "rendered: the web on 13000" "$work/config.yml" 'published: "13000"'
  else
    fail "docker compose config failed: $(head -c 300 "$work/config.err")"
  fi
elif [ -n "${STAGING_TEST_REQUIRE_COMPOSE:-}" ]; then
  fail "docker compose is required here (STAGING_TEST_REQUIRE_COMPOSE) but not available"
else
  echo "skip the rendered config checks: no docker compose here"
fi

if [ "$failures" -gt 0 ]; then
  echo "staging-test: $failures failed"
  exit 1
fi
echo "staging-test: all passed"
