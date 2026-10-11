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
#   infra-bump.sh open INFRA_DIR                       move the branch and the PR, or tidy the PR
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

# printf, never echo, for anything that carries text from commits: dash's echo reads backslashes.
die() { printf 'infra-bump: %s\n' "$*" >&2; exit 1; }
say() { printf 'infra-bump: %s\n' "$*"; }
usage() { die "usage: infra-bump.sh pinned|rewrite|decide|changes|open ... (see the script's header)"; }

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
  # Exactly one commit line; each image listed once under an `images:` key; and one digest line
  # inside each image's list item (the lines indented deeper than its "- name:"). Anything else
  # means the file changed shape, and the bump stops rather than guess.
  if ! awk -v commit="$2" -v api="$3" -v web="$4" -v migrate="$5" '
    function indent(s) { match(s, /^ */); return RLENGTH }
    function close_item() {
      if (image != "" && digests != 1) bad = 1
      image = ""
      digests = 0
    }
    BEGIN { want["jobpilot-api"] = api; want["jobpilot-web"] = web; want["jobpilot-migrate"] = migrate }
    $0 !~ /^[ \t]*(#.*)?$/ {
      i = indent($0)
      if (image != "" && i <= item_indent) close_item()
      # A list may sit at the same indentation as its key, so only a non-item line there ends it.
      if (in_images && (i < images_indent || (i == images_indent && $0 !~ /^ *- /))) in_images = 0
      if ($0 ~ /^ *images:[ \t]*$/) {
        in_images = 1
        images_indent = i
      } else if (in_images && $0 ~ /^ *-[ \t]*name:/) {
        name = $0
        sub(/^[ \t]*-[ \t]*name:[ \t]*/, "", name)
        sub(/[ \t]*$/, "", name)
        if (name in want) { image = name; item_indent = i; items[name]++ }
      }
    }
    image != "" && /^[ \t]*digest:/ { if (++digests == 1) sub(/digest:.*/, "digest: " want[image]) }
    /^[ \t]*commit:[ \t]*[0-9a-f]+[ \t]*$/ { sub(/commit:.*/, "commit: " commit); commits++ }
    { print }
    END {
      close_item()
      if (commits != 1) bad = 1
      for (n in want) if (items[n] != 1) bad = 1
      exit bad
    }' "$1" > "$tmp"; then
    rm -f "$tmp"
    die "$1: expected one pinned commit and one digest each for jobpilot-api, jobpilot-web and jobpilot-migrate"
  fi
  mv "$tmp" "$1"
}

cmd_decide() {
  [ $# -eq 2 ] || usage
  # A commit this history lacks can't be compared: call it a bump and let review decide.
  for c in "$1" "$2"; do
    if ! git cat-file -e "$c^{commit}" 2>/dev/null; then echo bump; return; fi
  done
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
  for c in "$1" "$2"; do
    if ! git cat-file -e "$c^{commit}" 2>/dev/null; then
      printf -- '- (%s is not in this history, so the list is unknown)\n' "$c"
      return
    fi
  done
  git log --first-parent --reverse --format=%H "$1..$2" | while read -r c; do
    subject=$(git log -1 --format=%s "$c")
    case $subject in
      'Merge pull request #'*)
        n=${subject#'Merge pull request #'}
        n=${n%% *}
        title=$(git log -1 --format=%b "$c" | sed -n '/./{p;q;}')
        printf -- '- %s#%s %s\n' "$3" "$n" "${title:-$subject}"
        ;;
      *) printf -- '- %s (%s)\n' "$subject" "$(short "$c")" ;;
    esac
  done
}

github() { # METHOD PATH [JSON]: the answer, never empty (jq -e reads no input as success)
  if [ $# -eq 3 ]; then
    set -- "$1" "$2" --data "$3"
  fi
  method=$1 path=$2
  shift 2
  out=$(curl -sS --fail-with-body -X "$method" \
    -H "Authorization: Bearer $GH_TOKEN" \
    -H 'Accept: application/vnd.github+json' \
    -H 'X-GitHub-Api-Version: 2022-11-28' \
    "$@" "${GITHUB_API_URL:-https://api.github.com}$path")
  [ -n "$out" ] || die "GitHub answered $method $path with nothing"
  printf '%s' "$out"
}

# The open PR's number from the lookup's answer, or nothing when there is none; any other answer
# fails the run, since reading it as "no PR" would open a second one or skip the checks below.
OPEN_PR='if type != "array" then error("not a list of pull requests")
  elif length == 0 then ""
  elif (.[0] | type == "object" and (.number | type) == "number" and .number > 0
         and (.number | floor) == .number and ((.body | type) == "string" or .body == null))
    then .[0].number | tostring
  else error("not a pull request: \(.[0])") end'

# describe RUNNING PROPOSED: the PR's title and body for that proposal. The body ends in a marker
# naming both commits, which is how a later run tells whether the description is still true.
describe() {
  list=$(cmd_changes "$1" "$2" "$JOBPILOT_REPO")
  title="JobPilot: run $(short "$2") on the lab"
  marker="<!-- infra-bump: running $1 proposed $2 -->"
  body="Runs jobpilot [\`$(short "$2")\`](https://github.com/$JOBPILOT_REPO/commit/$2) on the lab: \`deploy/base\` at that commit and its three images by digest, which jobpilot's CI built and published for it (the commit's page links the run).

Since the commit the lab runs now (\`$(short "$1")\`):

$list

Merging this PR deploys it: Flux applies \`$base\` within a minute. jobpilot's CI moves this PR forward on each \`master\` commit that changes what runs, so one merge can carry several of them, and closes it if \`$base\` gets ahead of it.

$marker"
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

  # Every answer from GitHub is captured and checked before it is used: in dash, curl | jq would
  # hide a failed call.
  running=$(cmd_pinned "$infra/$INFRA_FILE")
  move=$(cmd_decide "$running" "$SHA")
  answer=$(github GET "/repos/$INFRA_REPO/pulls?state=open&base=$base&head=${INFRA_REPO%%/*}:$BRANCH")
  number=$(printf '%s' "$answer" | jq -er "$OPEN_PR")
  if [ -n "$number" ]; then
    described=$(printf '%s' "$answer" | jq -r '.[0].body // ""')
    git -C "$infra" fetch -q --depth=1 origin "refs/heads/$BRANCH"
    seen=$(git -C "$infra" rev-parse FETCH_HEAD)
    git -C "$infra" show "$seen:$INFRA_FILE" > "$work/open.yaml"
    proposed=$(cmd_pinned "$work/open.yaml")
    # Never back to an older commit, nor for one deploying nothing beyond what the PR carries.
    if [ "$move" = bump ]; then move=$(cmd_decide "$proposed" "$SHA"); fi
  else
    seen=$(git -C "$infra" ls-remote origin "refs/heads/$BRANCH")
    seen=$(printf '%s' "$seen" | cut -f1)
  fi

  if [ "$move" = bump ]; then
    move_branch
    describe "$running" "$SHA"
    if [ -n "$number" ]; then
      answer=$(github PATCH "/repos/$INFRA_REPO/pulls/$number" \
        "$(jq -n --arg title "$title" --arg body "$body" '{title: $title, body: $body}')")
      url=$(pr_url "$answer")
      say "moved $url to $(short "$SHA")"
    else
      answer=$(github POST "/repos/$INFRA_REPO/pulls" \
        "$(jq -n --arg title "$title" --arg body "$body" --arg head "$BRANCH" --arg base "$base" \
          '{title: $title, body: $body, head: $head, base: $base}')")
      url=$(pr_url "$answer")
      say "opened $url"
    fi
    if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
      printf 'Bump PR in infra, running %s: %s\n' "$(short "$SHA")" "$url" >> "$GITHUB_STEP_SUMMARY"
    fi
    return
  fi

  if [ -z "$number" ]; then
    say "the lab runs $(short "$running"), and $(short "$SHA") deploys nothing newer: no bump"
    return
  fi
  # The branch stays. What the PR says must match what the branch proposes now, whatever an earlier
  # run left half done or someone else pushed; and a PR the lab has caught up with is closed.
  describe "$running" "$proposed"
  ahead=$(cmd_decide "$running" "$proposed")
  if [ "$ahead" != bump ]; then
    body="$body

Closed by jobpilot's CI: the lab already runs \`$(short "$running")\`, and this PR deploys nothing beyond it."
    answer=$(github PATCH "/repos/$INFRA_REPO/pulls/$number" \
      "$(jq -n --arg body "$body" '{state: "closed", body: $body}')")
    url=$(pr_url "$answer")
    say "closed $url: the lab already runs $(short "$running")"
    return
  fi
  case $described in
    *"$marker"*)
      say "infra#$number already proposes $(short "$proposed"), and $(short "$SHA") deploys nothing newer"
      ;;
    *)
      answer=$(github PATCH "/repos/$INFRA_REPO/pulls/$number" \
        "$(jq -n --arg title "$title" --arg body "$body" '{title: $title, body: $body}')")
      url=$(pr_url "$answer")
      say "described $url again: it proposes $(short "$proposed")"
      ;;
  esac
}

# move_branch: rebuild the branch from main with SHA and its digests, and push it. The lease is
# the tip this run looked at: a push from anywhere since then makes this one fail instead of
# overwriting it. Nothing else has changed at that point, and the next run describes whatever the
# branch then proposes.
move_branch() {
  answer=$(github GET "/users/$APP_SLUG%5Bbot%5D")
  bot_id=$(printf '%s' "$answer" | jq -er '.id')
  bot="${APP_SLUG}[bot]"
  case $bot_id in '' | *[!0-9]*) die "no user id for $bot: $bot_id" ;; esac
  list=$(cmd_changes "$running" "$SHA" "$JOBPILOT_REPO")
  git -C "$infra" checkout -q -B "$BRANCH"
  cmd_rewrite "$infra/$INFRA_FILE" "$SHA" "$API_DIGEST" "$WEB_DIGEST" "$MIGRATE_DIGEST"
  git -C "$infra" -c "user.name=$bot" -c "user.email=$bot_id+$bot@users.noreply.github.com" \
    commit -q -m "JobPilot: run $(short "$SHA") on the lab" -m "$list" -m "Images published by $RUN_URL" -- "$INFRA_FILE"
  git -C "$infra" push -q --force-with-lease="refs/heads/$BRANCH:$seen" origin "HEAD:refs/heads/$BRANCH"
}

pr_url() { # ANSWER: the pull request's address, or a failed run
  printf '%s' "$1" | jq -er '.html_url | select(type == "string" and startswith("https://"))'
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
