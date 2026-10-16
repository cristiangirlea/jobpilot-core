#!/bin/sh
# Staging on demand (#158): one throwaway copy of JobPilot on this PC, started for a test and
# removed afterwards. Run through make:
#   make staging-up REF=<branch|pr/N|commit>   build that commit and start it with sample data
#   make staging-status                        what is up, at which commit
#   make staging-down                          stop it and delete its database, images and checkout
# A branch name is taken from origin (what is on GitHub) when origin has it, pr/N is that pull
# request's head. There is one staging per clone, whichever worktree runs this: its state is in
# .git/jobpilot-staging/ (shared by the clone's worktrees, never seen by a checkout), the commit is
# checked out in its src/, and it runs as the Compose project jobpilot-staging with this
# checkout's deploy/staging/compose.override.yml.
# Compose runs with a stripped environment (only what Docker itself needs), so no setting comes
# from the shell or a .env. Loopback only: web on STAGING_WEB_PORT (13000), API on
# STAGING_API_PORT (18081); 3000, 8080 and 5432 are refused. The login is written to
# .git/jobpilot-staging/login.txt, never printed. STAGING_DRY_RUN=1 prints the commands instead of running them.
# Needs git 2.31+, Docker Compose 2.24+ and curl.
set -eu
# Git Bash rewrites arguments that look like POSIX paths (/app/seed) into Windows ones before
# Docker sees them; the paths here come from git already in a form Docker takes.
MSYS_NO_PATHCONV=1
export MSYS_NO_PATHCONV

root=$(git rev-parse --show-toplevel)
common=$(git rev-parse --path-format=absolute --git-common-dir)
dir="$common/jobpilot-staging"
src="$dir/src"
project=jobpilot-staging
override="$root/deploy/staging/compose.override.yml"
email=staging@jobpilot.test
STAGING_WEB_PORT=${STAGING_WEB_PORT:-13000}
STAGING_API_PORT=${STAGING_API_PORT:-18081}
dry=${STAGING_DRY_RUN:-}

die() { echo "staging: $*" >&2; exit 1; }
run() { if [ -n "$dry" ]; then echo "+ $*"; else "$@"; fi; }

# docker with only the variables Docker needs to find its engine and plugins, and the ports.
docker_clean() {
  if [ -n "$dry" ]; then
    echo "+ env -i STAGING_WEB_PORT=$STAGING_WEB_PORT STAGING_API_PORT=$STAGING_API_PORT <docker's own variables> $*"
    return 0
  fi
  for v in PATH HOME USERPROFILE SYSTEMROOT PROGRAMFILES PROGRAMDATA APPDATA LOCALAPPDATA TEMP TMP \
    DOCKER_HOST DOCKER_CONTEXT DOCKER_CONFIG DOCKER_CERT_PATH DOCKER_TLS_VERIFY MSYS_NO_PATHCONV; do
    eval "x=\${$v-}"
    [ -z "$x" ] || set -- "$v=$x" "$@"
  done
  env -i STAGING_WEB_PORT="$STAGING_WEB_PORT" STAGING_API_PORT="$STAGING_API_PORT" "$@"
}
compose() {
  docker_clean docker compose -p "$project" --project-directory "$src" -f "$src/docker-compose.yml" -f "$override" "$@"
}

check_port() { # PORT NAME
  case $1 in
    '' | *[!0-9]* | 0*) die "$2 must be a port number from 1 to 65535 without leading zeros, not '$1'" ;;
  esac
  [ "$1" -le 65535 ] || die "$2 must be a port number from 1 to 65535, not '$1'"
  case $1 in
    3000 | 8080 | 5432) die "$2 $1 is the owner's stack's port; staging never uses 3000, 8080 or 5432" ;;
  esac
  [ -z "$dry" ] || return 0
  # Free means refused (curl exit 7); Windows takes about 2.5 s to refuse a loopback port.
  rc=0
  curl -s -o /dev/null -m 6 "http://127.0.0.1:$1/" || rc=$?
  [ "$rc" = 7 ] || die "$2 $1 is already in use on this PC; free it or set $2 to another port"
}

resolve() { # REF: prints the commit
  case $1 in
    pr/*)
      n=${1#pr/}
      case $n in '' | *[!0-9]*) die "pr/N needs a pull request number, not '$1'" ;; esac
      git -C "$root" fetch -q origin "pull/$n/head" || die "cannot fetch pull request $n from origin"
      git -C "$root" rev-parse FETCH_HEAD
      ;;
    *)
      git -C "$root" fetch -q origin || echo "staging: cannot fetch from origin; using what this clone has" >&2
      git -C "$root" rev-parse -q --verify "origin/$1^{commit}" ||
        git -C "$root" rev-parse -q --verify "$1^{commit}" ||
        die "unknown branch, pull request or commit: $1"
      ;;
  esac
}

up() {
  [ $# -eq 1 ] && [ -n "$1" ] || die "usage: make staging-up REF=<branch|pr/N|commit>"
  [ ! -e "$src" ] || die "staging is already up ($(cat "$dir/ref" 2>/dev/null || echo 'unknown commit')); run make staging-down first"
  check_port "$STAGING_WEB_PORT" STAGING_WEB_PORT
  check_port "$STAGING_API_PORT" STAGING_API_PORT
  [ "$STAGING_WEB_PORT" != "$STAGING_API_PORT" ] || die "STAGING_WEB_PORT and STAGING_API_PORT must differ"
  [ -n "$dry" ] || command -v curl > /dev/null || die "needs curl"
  sha=$(resolve "$1") || exit 1
  echo "staging $1 at $(echo "$sha" | cut -c1-12)"

  run mkdir -p "$dir"
  run git -C "$root" worktree add -q --detach "$src" "$sha"
  [ -n "$dry" ] || echo "$1 $sha" > "$dir/ref"
  trap 'echo "staging: up did not finish; make staging-down cleans up what it started" >&2' EXIT
  compose up -d --build --wait

  url="http://localhost:$STAGING_WEB_PORT"
  if [ -n "$dry" ]; then
    echo "+ create $email through POST /api/auth/register, login in $dir/login.txt"
  else
    password=$(head -c 18 /dev/urandom | base64 | tr -d '/+=\n')
    (umask 077 && printf 'url %s\nemail %s\npassword %s\n' "$url" "$email" "$password" > "$dir/login.txt")
    printf '{"email":"%s","password":"%s"}' "$email" "$password" |
      curl -sf -o /dev/null -X POST -H 'Content-Type: application/json' --data-binary @- \
        "http://127.0.0.1:$STAGING_API_PORT/api/auth/register" || die "could not create the staging user"
  fi
  compose run --rm --no-deps --entrypoint /app/seed api
  trap - EXIT
  echo "staging is up: $url (login in $dir/login.txt)"
}

down() {
  if [ -e "$src/docker-compose.yml" ]; then
    compose down -v --rmi local --remove-orphans
  else
    # No checkout left: name the project only, from a directory without a compose file or .env.
    if [ -n "$dry" ]; then
      docker_clean docker compose -p "$project" down -v --rmi local --remove-orphans
    else
      mkdir -p "$dir"
      (cd "$dir" && docker_clean docker compose -p "$project" down -v --rmi local --remove-orphans)
    fi
  fi
  if [ -e "$src" ]; then run git -C "$root" worktree remove --force "$src"; fi
  run git -C "$root" worktree prune
  [ -n "$dry" ] || rm -f "$dir/ref" "$dir/login.txt"
  echo "staging is down"
}

status() {
  if [ -f "$dir/ref" ]; then
    echo "staging is up: $(cat "$dir/ref"), http://localhost:$STAGING_WEB_PORT (login in $dir/login.txt)"
    docker_clean docker compose -p "$project" ps --format '{{.Service}}\t{{.Status}}'
  elif [ -n "$dry" ]; then
    echo "staging is down"
  elif [ -n "$(docker_clean docker compose ls -a -q --filter name=$project)" ]; then
    echo "staging has containers but no recorded commit (left over); make staging-down removes them"
  else
    echo "staging is down"
  fi
}

case ${1:-} in
  up) shift && up "$@" ;;
  down) down ;;
  status) status ;;
  *) echo "usage: staging.sh up <branch|pr/N|commit> | down | status" >&2 && exit 2 ;;
esac
