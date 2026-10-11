#!/bin/sh
# Moves the lab cluster to a jobpilot commit through the rolling bump PR in the infra repository:
# branch bump/jobpilot, rebuilt from infra's main each time, pins clusters/local/jobpilot.yaml to the
# commit (deploy/base is read from it) and its three images by digest. CI's `infra bump PR` job runs
# `open`; the other commands are its parts. Merging the PR is the release, and it is the owner's.
#
#   infra-bump.sh pinned FILE                          print the commit FILE pins
#   infra-bump.sh rewrite FILE COMMIT API WEB MIGRATE  pin COMMIT and the three image digests in FILE
#   infra-bump.sh decide PINNED SHA                    print bump, same (nothing deployed changed)
#                                                      or older (SHA is behind PINNED)
#   infra-bump.sh changes PINNED SHA REPO              the first-parent commits PINNED..SHA, in Markdown
#   infra-bump.sh open INFRA_DIR                       push the branch, open or update the PR
#
# decide, changes and open read the git history of the jobpilot checkout they run in. open takes
# INFRA_DIR, a checkout of infra's main whose origin it may push to, and these variables: SHA,
# API_DIGEST, WEB_DIGEST, MIGRATE_DIGEST, GH_TOKEN, APP_SLUG (whose bot commits), INFRA_REPO and
# JOBPILOT_REPO (owner/name), RUN_URL, and optionally GITHUB_API_URL and INFRA_BASE (main). It
# calls GitHub's REST API with curl and reads the answers with jq.
#
# Tested by scripts/infra-bump-test.sh. POSIX sh: CI runs it with dash.
set -eu

# What reaches the cluster: the three images' build contexts and the manifests Flux reads.
DEPLOYED='apps/api apps/web migrations deploy'
INFRA_FILE=clusters/local/jobpilot.yaml
BRANCH=bump/jobpilot

die() { echo "infra-bump: $*" >&2; exit 1; }
say() { echo "infra-bump: $*"; }
usage() { echo "usage: infra-bump.sh pinned|rewrite|decide|changes|open ... (see the script's header)" >&2; exit 2; }

# is_hex VALUE LENGTH: exactly LENGTH lowercase hex digits, so nothing else reaches the YAML.
is_hex() {
  [ "${#1}" -eq "$2" ] || return 1
  case $1 in *[!0-9a-f]*) return 1 ;; esac
}
is_digest() {
  case $1 in sha256:*) is_hex "${1#sha256:}" 64 ;; *) return 1 ;; esac
}
short() { printf '%s' "$1" | cut -c1-7; }

cmd_pinned() {
  found=$(sed -n 's/^[ 	]*commit:[ 	]*\([0-9a-f]\{40\}\)[ 	]*$/\1/p' "$1")
  if [ -z "$found" ] || [ "$(printf '%s\n' "$found" | wc -l)" -ne 1 ]; then
    die "$1: expected one pinned commit, found ${found:-none}"
  fi
  echo "$found"
}

cmd_rewrite() {
  [ $# -eq 5 ] || usage
  is_hex "$2" 40 || die "not a commit SHA: $2"
  for d in "$3" "$4" "$5"; do is_digest "$d" || die "not an image digest: $d"; done
  tmp="$1.bump.$$"
  # Exactly one commit line, and for each image one digest line in its own list item; anything
  # else means the file changed shape, and the bump stops rather than guess.
  if ! awk -v commit="$2" -v api="$3" -v web="$4" -v migrate="$5" '
    BEGIN { want["jobpilot-api"] = api; want["jobpilot-web"] = web; want["jobpilot-migrate"] = migrate }
    /^[^ \t#]/ { image = "" }
    /^[ \t]*commit:[ \t]*[0-9a-f]+[ \t]*$/ { sub(/commit:.*/, "commit: " commit); commits++ }
    /^[ \t]*-[ \t]*name:/ {
      name = $0
      sub(/^[ \t]*-[ \t]*name:[ \t]*/, "", name)
      sub(/[ \t]*$/, "", name)
      image = (name in want) ? name : ""
    }
    image != "" && /^[ \t]*digest:/ { sub(/digest:.*/, "digest: " want[image]); got[image]++; image = "" }
    { print }
    END {
      bad = commits != 1
      for (n in want) if (got[n] != 1) bad = 1
      exit bad
    }' "$1" > "$tmp"; then
    rm -f "$tmp"
    die "$1: expected one pinned commit and one digest each for jobpilot-api, jobpilot-web and jobpilot-migrate"
  fi
  mv "$tmp" "$1"
}

cmd_decide() {
  [ $# -eq 2 ] || usage
  # A pinned commit this history lacks can't be compared: propose the bump and let review decide.
  if ! git cat-file -e "$1^{commit}" 2>/dev/null; then echo bump; return; fi
  if [ "$1" = "$2" ]; then echo same; return; fi
  if git merge-base --is-ancestor "$2" "$1"; then echo older; return; fi
  # shellcheck disable=SC2086 # DEPLOYED is a list of paths
  if git diff --quiet "$1" "$2" -- $DEPLOYED; then
    echo same
  else
    rc=$?
    [ "$rc" -eq 1 ] || die "git diff $1 $2 failed ($rc)"
    echo bump
  fi
}

cmd_changes() {
  [ $# -eq 3 ] || usage
  if ! git cat-file -e "$1^{commit}" 2>/dev/null; then
    echo "- (the commit the lab runs, $1, is not in this history)"
    return
  fi
  git log --first-parent --reverse --format=%H "$1..$2" | while read -r c; do
    subject=$(git log -1 --format=%s "$c")
    case $subject in
      'Merge pull request #'*)
        n=${subject#'Merge pull request #'}
        n=${n%% *}
        title=$(git log -1 --format=%b "$c" | sed -n '/./{p;q;}')
        echo "- $3#$n ${title:-$subject}"
        ;;
      *) echo "- $subject ($(short "$c"))" ;;
    esac
  done
}

github() { # METHOD PATH [JSON]
  if [ $# -eq 3 ]; then
    set -- "$1" "$2" --data "$3"
  fi
  method=$1 path=$2
  shift 2
  curl -sS --fail-with-body -X "$method" \
    -H "Authorization: Bearer $GH_TOKEN" \
    -H 'Accept: application/vnd.github+json' \
    -H 'X-GitHub-Api-Version: 2022-11-28' \
    "$@" "${GITHUB_API_URL:-https://api.github.com}$path"
}

cmd_open() {
  [ $# -eq 1 ] || usage
  infra=$1
  for v in SHA API_DIGEST WEB_DIGEST MIGRATE_DIGEST GH_TOKEN APP_SLUG INFRA_REPO JOBPILOT_REPO RUN_URL; do
    eval "[ -n \"\${$v:-}\" ]" || die "$v is not set"
  done
  base=${INFRA_BASE:-main}
  work=$(mktemp -d)
  trap 'rm -rf "$work"' EXIT

  running=$(cmd_pinned "$infra/$INFRA_FILE")
  decision=$(cmd_decide "$running" "$SHA")
  case $decision in
    same) say "the lab runs $(short "$running"), and nothing deployed changed since: no bump"; return ;;
    older) say "the lab runs $(short "$running"), newer than $(short "$SHA"): no bump" ; return ;;
  esac

  # One PR, moved forward: never back to an older commit, and not for a commit that deploys
  # nothing the PR doesn't already carry.
  owner=${INFRA_REPO%%/*}
  number=$(github GET "/repos/$INFRA_REPO/pulls?state=open&base=$base&head=$owner:$BRANCH" | jq -r '.[0].number // empty')
  if [ -n "$number" ]; then
    git -C "$infra" fetch -q --depth=1 origin "refs/heads/$BRANCH"
    git -C "$infra" show "FETCH_HEAD:$INFRA_FILE" > "$work/open.yaml"
    proposed=$(cmd_pinned "$work/open.yaml")
    decision=$(cmd_decide "$proposed" "$SHA")
    case $decision in
      same | older)
        say "infra#$number already proposes $(short "$proposed"), and $(short "$SHA") deploys nothing newer"
        return
        ;;
    esac
  fi

  bot_id=$(github GET "/users/$APP_SLUG%5Bbot%5D" | jq -r '.id')
  bot="${APP_SLUG}[bot]"
  case $bot_id in '' | *[!0-9]*) die "no user id for $bot: $bot_id" ;; esac

  list=$(cmd_changes "$running" "$SHA" "$JOBPILOT_REPO")
  title="JobPilot: run $(short "$SHA") on the lab"
  git -C "$infra" checkout -q -B "$BRANCH"
  cmd_rewrite "$infra/$INFRA_FILE" "$SHA" "$API_DIGEST" "$WEB_DIGEST" "$MIGRATE_DIGEST"
  git -C "$infra" -c "user.name=$bot" -c "user.email=$bot_id+$bot@users.noreply.github.com" \
    commit -q -m "$title" -m "$list" -- "$INFRA_FILE"
  # The branch belongs to this job and is rebuilt from main each time; the lease only refuses a
  # push that would overwrite something pushed since the lookup above.
  remote=$(git -C "$infra" ls-remote origin "refs/heads/$BRANCH" | cut -f1)
  git -C "$infra" push -q --force-with-lease="refs/heads/$BRANCH:$remote" origin "HEAD:refs/heads/$BRANCH"

  body="Runs jobpilot [\`$(short "$SHA")\`](https://github.com/$JOBPILOT_REPO/commit/$SHA) on the lab: \`deploy/base\` at that commit and its three images by digest, built and pushed by [this CI run]($RUN_URL).

Since the commit the lab runs now (\`$(short "$running")\`):

$list

Merging this PR deploys it: Flux applies \`$base\` within a minute. jobpilot's CI moves this PR forward on each \`master\` commit that changes what runs, so one merge can carry several of them."
  if [ -n "$number" ]; then
    url=$(github PATCH "/repos/$INFRA_REPO/pulls/$number" \
      "$(jq -n --arg title "$title" --arg body "$body" '{title: $title, body: $body}')" | jq -r '.html_url')
    say "moved $url to $(short "$SHA")"
  else
    url=$(github POST "/repos/$INFRA_REPO/pulls" \
      "$(jq -n --arg title "$title" --arg body "$body" --arg head "$BRANCH" --arg base "$base" \
        '{title: $title, body: $body, head: $head, base: $base}')" | jq -r '.html_url')
    say "opened $url"
  fi
  if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
    echo "Bump PR in infra, running $(short "$SHA"): $url" >> "$GITHUB_STEP_SUMMARY"
  fi
}

[ $# -gt 0 ] || usage
command=$1
shift
case $command in
  pinned) [ $# -eq 1 ] || usage; cmd_pinned "$1" ;;
  rewrite) cmd_rewrite "$@" ;;
  decide) cmd_decide "$@" ;;
  changes) cmd_changes "$@" ;;
  open) cmd_open "$@" ;;
  *) usage ;;
esac
