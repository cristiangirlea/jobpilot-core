#!/bin/sh
# Tests scripts/infra-bump.sh: the file rewrite and what it refuses, the bump decision, the change
# list, and `open` end to end, against a local bare repository standing in for infra and a stub curl
# standing in for GitHub's API, which keeps the PR between runs. Run with sh from anywhere (CI uses
# dash); needs git and jq.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
bump="$here/infra-bump.sh"
fixture="$here/testdata/infra-jobpilot.yaml"
fixture_commit=8bd96292630be44d7c3b4dc5a2eb97f307ca02a9
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0

command -v jq > /dev/null || { echo "infra-bump-test: needs jq"; exit 1; }

pass() { echo "ok   $1"; }
fail() { echo "FAIL $1"; failures=$((failures + 1)); }
expect() { # DESCRIPTION WANT GOT
  if [ "$2" = "$3" ]; then pass "$1"; else fail "$1: want [$2], got [$3]"; fi
}
refuses() { # DESCRIPTION COMMAND...
  d=$1
  shift
  if "$@" > "$work/refused.out" 2>&1; then fail "$d: it succeeded"; else pass "$d"; fi
}
unchanged() { # DESCRIPTION FILE COPY
  if cmp -s "$2" "$3"; then pass "$1"; else fail "$1"; fi
}
hex40() { printf '%040d' "$1"; }
digest() { printf 'sha256:%064d' "$1"; }
short() { printf '%s' "$1" | cut -c1-7; }
image() { # FILE NAME: the digest pinned for that image
  awk -v n="$2" '$0 ~ "- name: " n "$" { f = 1 } f && /digest:/ { print $2; exit }' "$1"
}
rewrite_ok() { # FILE: pins hex40 1 and digests 2, 3, 4
  sh "$bump" rewrite "$1" "$(hex40 1)" "$(digest 2)" "$(digest 3)" "$(digest 4)"
}

# --- pinned and rewrite, on copies of infra's clusters/local/jobpilot.yaml

expect "pinned reads the commit" "$fixture_commit" "$(sh "$bump" pinned "$fixture")"

cp "$fixture" "$work/a.yaml"
rewrite_ok "$work/a.yaml"
expect "rewrite pins the commit" "$(hex40 1)" "$(sh "$bump" pinned "$work/a.yaml")"
expect "rewrite pins the API image" "$(digest 2)" "$(image "$work/a.yaml" jobpilot-api)"
expect "rewrite pins the web image" "$(digest 3)" "$(image "$work/a.yaml" jobpilot-web)"
expect "rewrite pins the migrate image" "$(digest 4)" "$(image "$work/a.yaml" jobpilot-migrate)"
expect "rewrite changes those four lines and nothing else" 4 "$(diff "$fixture" "$work/a.yaml" | grep -c '^>')"

awk '{ print } /- name: jobpilot-web$/ { print ""; print "      # a note" }' "$fixture" > "$work/a2.yaml"
rewrite_ok "$work/a2.yaml" || fail "rewrite of an image item with a blank line and a comment failed"
expect "rewrite reads past blank and comment lines in an item" "$(digest 3)" "$(image "$work/a2.yaml" jobpilot-web)"

awk '{ print } /- name: lab-jobpilot-infra$/ { print "    - name: jobpilot-api" }' "$fixture" > "$work/a3.yaml"
rewrite_ok "$work/a3.yaml" || fail "rewrite with an image's name in dependsOn failed"
expect "rewrite counts only the images: list" "$(digest 2)" "$(image "$work/a3.yaml" jobpilot-api)"

# The images list at its key's own indentation, which YAML allows.
awk 'f { sub(/^  /, "") } { print } /^  images:$/ { f = 1 }' "$fixture" > "$work/a4.yaml"
expect "the fixture can put the list at its key's indentation" 1 "$(grep -c '^  - name: jobpilot-migrate$' "$work/a4.yaml")"
rewrite_ok "$work/a4.yaml" || fail "rewrite of a list at its key's indentation failed"
expect "rewrite reads a list at its key's indentation" "$(digest 4)" "$(image "$work/a4.yaml" jobpilot-migrate)"

cp "$fixture" "$work/b.yaml"
refuses "rewrite refuses a short commit" sh "$bump" rewrite "$work/b.yaml" 8bd9629 "$(digest 2)" "$(digest 3)" "$(digest 4)"
refuses "rewrite refuses an uppercase commit" sh "$bump" rewrite "$work/b.yaml" "$(hex40 1 | tr 0 A)" "$(digest 2)" "$(digest 3)" "$(digest 4)"
refuses "rewrite refuses a digest without sha256:" sh "$bump" rewrite "$work/b.yaml" "$(hex40 1)" "$(printf '%064d' 2)" "$(digest 3)" "$(digest 4)"
refuses "rewrite refuses a short digest" sh "$bump" rewrite "$work/b.yaml" "$(hex40 1)" "$(digest 2)" sha256:abc "$(digest 4)"
refuses "rewrite refuses a digest carrying a second line" sh "$bump" rewrite "$work/b.yaml" "$(hex40 1)" "$(digest 2)" "$(digest 3)" "$(digest 4)
      digest: $(digest 9)"
refuses "rewrite refuses a missing argument" sh "$bump" rewrite "$work/b.yaml" "$(hex40 1)" "$(digest 2)" "$(digest 3)"
unchanged "a refused rewrite leaves the file as it was" "$fixture" "$work/b.yaml"

# malformed FILE DESCRIPTION: rewrite refuses FILE and leaves it as it was
malformed() {
  cp "$1" "$1.orig"
  refuses "rewrite refuses $2" rewrite_ok "$1"
  unchanged "and leaves that file as it was" "$1.orig" "$1"
}
grep -v 'name: jobpilot-migrate$' "$fixture" > "$work/c.yaml"
malformed "$work/c.yaml" "a file without one of the images"
awk '{ print } /- name: jobpilot-api$/ { f = 1 } f && /digest:/ { print "    - name: jobpilot-api"; print; f = 0 }' "$fixture" > "$work/d.yaml"
malformed "$work/d.yaml" "an image listed twice"
awk '{ print } /- name: jobpilot-web$/ { print "    - name: jobpilot-web" }' "$fixture" > "$work/d2.yaml"
malformed "$work/d2.yaml" "an image listed again without a digest"
awk '{ print } /- name: jobpilot-web$/ { f = 1 } f && /digest:/ { print; f = 0 }' "$fixture" > "$work/d3.yaml"
malformed "$work/d3.yaml" "an image with two digests"
sed '$d' "$fixture" > "$work/d4.yaml"
malformed "$work/d4.yaml" "the last image without its digest"
sed '$d' "$fixture" > "$work/d5.yaml"
printf '  digest: %s\n' "$(digest 5)" >> "$work/d5.yaml"
malformed "$work/d5.yaml" "the last image's digest outside its item"
awk '{ print } /^ *commit: / { print }' "$fixture" > "$work/e.yaml"
malformed "$work/e.yaml" "two pinned commits"
refuses "pinned refuses two pinned commits" sh "$bump" pinned "$work/e.yaml"

# --- decide and changes, on a small history shaped like jobpilot's

repo="$work/jobpilot"
git init -q -b master "$repo"
g() { git -C "$repo" -c user.name=test -c user.email=test@example.com -c commit.gpgsign=false "$@"; }
commit_file() { # PATH SUBJECT: prints the new commit
  mkdir -p "$repo/$(dirname "$1")"
  printf '%s\n' "$2" >> "$repo/$1"
  g add -A
  g commit -q -m "$2"
  g rev-parse HEAD
}
merge_branch() { # BRANCH PATH NUMBER TITLE: a merged pull request, as GitHub merges one
  g checkout -q -b "$1"
  commit_file "$2" "Change on $1" > /dev/null
  g checkout -q master
  g merge -q --no-ff "$1" -m "Merge pull request #$3 from owner/$1" -m "$4"
  g rev-parse HEAD
}
# Text that would break echo or a shell: backslash escapes, quotes, backticks, $(...).
# shellcheck disable=SC2016 # literally
nasty='Keep \n, \c, "quotes", `ticks` and $(echo hi) as written'
plain='Web again, with \t and \\ kept'
c1=$(commit_file apps/api/main.go "Start")
c2=$(commit_file docs/notes.md "Docs only")
c3=$(merge_branch web apps/web/page.tsx 7 "Show the web change")
c4=$(commit_file deploy/base/api.yaml "Deploy change")
c5=$(commit_file apps/mcp/main.go "MCP only")
c6=$(commit_file migrations/019_example.sql "A migration")
c7=$(merge_branch api apps/api/handler.go 8 "$nasty")
c8=$(commit_file apps/web/page.tsx "$plain")
c9=$(commit_file migrations/020_example.sql "Another migration")
c10=$(commit_file apps/web/page.tsx "Web change for the last PR")

decide() { (cd "$repo" && sh "$bump" decide "$1" "$2"); }
expect "decide: a docs-only commit is the same" same "$(decide "$c1" "$c2")"
expect "decide: a merged web change is a bump" bump "$(decide "$c1" "$c3")"
expect "decide: a deploy/ change is a bump" bump "$(decide "$c3" "$c4")"
expect "decide: apps/mcp is not deployed" same "$(decide "$c4" "$c5")"
expect "decide: a migration is a bump" bump "$(decide "$c5" "$c6")"
expect "decide: an apps/api change is a bump" bump "$(decide "$c6" "$c7")"
expect "decide: the running commit itself is the same" same "$(decide "$c3" "$c3")"
expect "decide: an ancestor of the running commit is older" older "$(decide "$c4" "$c1")"
expect "decide: a running commit this history lacks is a bump" bump "$(decide "$(hex40 7)" "$c1")"
expect "decide: a proposed commit this history lacks is a bump" bump "$(decide "$c1" "$(hex40 7)")"

changes() { (cd "$repo" && sh "$bump" changes "$1" "$2" owner/jobpilot); }
expect "changes lists merged PRs by number and title, other commits by subject" "- Docs only ($(short "$c2"))
- owner/jobpilot#7 Show the web change
- Deploy change ($(short "$c4"))" "$(changes "$c1" "$c4")"
expect "changes keeps a PR title exactly as written" "- owner/jobpilot#8 $nasty" "$(changes "$c6" "$c7")"
expect "changes keeps a commit subject exactly as written" "- $plain ($(short "$c8"))" "$(changes "$c7" "$c8")"

# --- open, end to end: infra's main pins c1 to begin with

bare="$work/infra.git"
git init -q --bare -b main "$bare"
git clone -q "$bare" "$work/seed" 2> /dev/null
mkdir -p "$work/seed/clusters/local"
sed "s/$fixture_commit/$c1/" "$fixture" > "$work/seed/clusters/local/jobpilot.yaml"
git -C "$work/seed" add -A
git -C "$work/seed" -c user.name=test -c user.email=test@example.com -c commit.gpgsign=false commit -q -m "JobPilot at the start"
git -C "$work/seed" push -q origin HEAD:main
main=$(git -C "$bare" rev-parse main)

# The stub API logs each call as "METHOD URL" in $STUB/calls and keeps the JSON it was sent in
# $STUB/data.<call number>. It keeps the open PR in $STUB_STATE/pr.json across runs (a closed one
# moves to closed.json). A call matching $STUB_FAIL fails as curl --fail-with-body does, with
# $STUB_FAIL_BODY and exit 22; one matching $STUB_ANSWER gets $STUB_ANSWER_BODY and exit 0;
# $STUB_ON_USER runs before the bot's user is looked up, between the branch fetch and the push; and
# $STUB_ON_PATCH runs before a PATCH is answered.
mkdir -p "$work/bin" "$work/state"
cat > "$work/bin/curl" << 'EOF'
#!/bin/sh
method=GET data= url=
while [ $# -gt 0 ]; do
  case $1 in
    -X) method=$2; shift ;;
    --data) data=$2; shift ;;
    -H) shift ;;
    -*) ;;
    *) url=$1 ;;
  esac
  shift
done
n=$(($(cat "$STUB/count" 2> /dev/null || echo 0) + 1))
echo "$n" > "$STUB/count"
echo "$method $url" >> "$STUB/calls"
printf '%s' "$data" > "$STUB/data.$n"
call="$method $url"
# shellcheck disable=SC2254 # the variables are patterns
if [ -n "$STUB_FAIL" ]; then case $call in $STUB_FAIL) printf '%s' "$STUB_FAIL_BODY"; exit 22 ;; esac; fi
# shellcheck disable=SC2254
if [ -n "$STUB_ANSWER" ]; then case $call in $STUB_ANSWER) printf '%s' "$STUB_ANSWER_BODY"; exit 0 ;; esac; fi
pr="$STUB_STATE/pr.json"
case $call in
  "GET "*/users/jobpilot-bump%5Bbot%5D)
    if [ -n "$STUB_ON_USER" ]; then sh -c "$STUB_ON_USER" > /dev/null 2>&1; fi
    echo '{"login": "jobpilot-bump[bot]", "id": 42}'
    ;;
  "GET "*/repos/owner/infra/pulls\?*)
    if [ -f "$pr" ]; then jq -c '[.]' "$pr"; else echo '[]'; fi
    ;;
  "POST "*/repos/owner/infra/pulls)
    printf '%s' "$data" | jq -c '{number: 9, state: "open", html_url: "https://github.example/owner/infra/pull/9"} + .' > "$pr"
    cat "$pr"
    ;;
  "PATCH "*/repos/owner/infra/pulls/9)
    if [ -n "$STUB_ON_PATCH" ]; then sh -c "$STUB_ON_PATCH" > /dev/null 2>&1; fi
    # GitHub edits a closed PR too, and reopens it on {"state": "open"}.
    if [ ! -f "$pr" ] && [ -f "$STUB_STATE/closed.json" ]; then mv "$STUB_STATE/closed.json" "$pr"; fi
    [ -f "$pr" ] || { echo '{"message": "Not Found"}'; exit 22; }
    printf '%s' "$data" | jq -c --slurpfile old "$pr" '$old[0] + .' > "$pr.new"
    mv "$pr.new" "$pr"
    if [ "$(jq -r .state "$pr")" = closed ]; then mv "$pr" "$STUB_STATE/closed.json"; cat "$STUB_STATE/closed.json"; else cat "$pr"; fi
    ;;
  *) echo "stub curl: unexpected $call" >&2; exit 22 ;;
esac
EOF
chmod +x "$work/bin/curl"

normal() { fail_on='' fail_body='{"message": "Server Error"}' answer_on='' answer_body='' on_user='' on_patch=''; }
normal
open() { # SHA RUN: a fresh checkout of infra's main, as each CI run has; RUN numbers the digests
  rm -rf "$work/infra" "$work/api"
  mkdir -p "$work/api"
  git clone -q "$bare" "$work/infra" 2> /dev/null
  (cd "$repo" && PATH="$work/bin:$PATH" STUB="$work/api" STUB_STATE="$work/state" \
    STUB_FAIL="$fail_on" STUB_FAIL_BODY="$fail_body" STUB_ANSWER="$answer_on" STUB_ANSWER_BODY="$answer_body" \
    STUB_ON_USER="$on_user" STUB_ON_PATCH="$on_patch" GITHUB_API_URL=https://api.github.example \
    SHA=$1 API_DIGEST="$(digest "${2}1")" WEB_DIGEST="$(digest "${2}2")" MIGRATE_DIGEST="$(digest "${2}3")" \
    GH_TOKEN=test-token APP_SLUG=jobpilot-bump INFRA_REPO=owner/infra JOBPILOT_REPO=owner/jobpilot \
    RUN_URL="https://github.example/owner/jobpilot/actions/runs/$2" sh "$bump" open "$work/infra")
}
opens() { # DESCRIPTION OPEN-ARGUMENTS...: the run succeeds
  d=$1
  shift
  if ! open "$@" > "$work/out" 2>&1; then
    cat "$work/out"
    fail "$d: open failed"
  fi
}
open_fails() { # DESCRIPTION OPEN-ARGUMENTS...: the run fails
  d=$1
  shift
  if open "$@" > "$work/out" 2>&1; then fail "$d: open succeeded"; else pass "$d"; fi
}
calls() { # METHOD: how many such calls the last run made
  if [ -f "$work/api/calls" ]; then grep -c "^$1 " "$work/api/calls" || true; else echo 0; fi
}
last() { # METHOD: the JSON the last run's last such call sent
  n=$(grep -n "^$1 " "$work/api/calls" | tail -1 | cut -d: -f1)
  cat "$work/api/data.$n"
}
pinned_on() { # BRANCH: the commit infra's BRANCH pins
  git -C "$bare" show "$1:clusters/local/jobpilot.yaml" > "$work/pinned.yaml"
  sh "$bump" pinned "$work/pinned.yaml"
}
proposed() { pinned_on bump/jobpilot; }
pr_state() { if [ -f "$work/state/pr.json" ]; then echo open; else echo none; fi; }
described() { # DESCRIPTION: the open PR says what the branch proposes now, over what main runs
  r=$(pinned_on main)
  p=$(proposed)
  t=$(jq -r .title "$work/state/pr.json" 2> /dev/null || true)
  if [ "$t" = "JobPilot: run $(short "$p") on the lab" ] &&
    jq -r .body "$work/state/pr.json" | grep -Fqx "<!-- infra-bump: running $r proposed $p -->"; then
    pass "$1"
  else
    fail "$1: the PR says [$t], the branch proposes $(short "$p") over $(short "$r")"
  fi
}

# 1. The first bump opens the PR.
opens "a first bump" "$c3" 1
expect "open: a first bump opens a PR" "1 0" "$(calls POST) $(calls PATCH)"
expect "open: it looks the PR up by branch and base" 1 "$(grep -c '/repos/owner/infra/pulls?state=open&base=main&head=owner:bump/jobpilot$' "$work/api/calls")"
expect "open: the PR goes from bump/jobpilot to main" "bump/jobpilot main" "$(last POST | jq -r '.head + " " + .base')"
expect "open: the PR lists the jobpilot PRs it carries" 1 "$(last POST | jq -r .body | grep -c '^- owner/jobpilot#7 Show the web change$')"
expect "open: the PR links the run that published the images" 1 "$(last POST | jq -r .body | grep -Fc '[this CI run](https://github.example/owner/jobpilot/actions/runs/1)')"
described "open: the PR says what the branch proposes"
expect "open: the branch pins the commit's images" "$(digest 11)" "$(proposed > /dev/null && image "$work/pinned.yaml" jobpilot-api)"
expect "open: the branch is one commit on main" 1 "$(git -C "$bare" rev-list --count main..bump/jobpilot)"
expect "open: the commit is the App's bot's" "jobpilot-bump[bot] <42+jobpilot-bump[bot]@users.noreply.github.com>" "$(git -C "$bare" log -1 --format='%an <%ae>' bump/jobpilot)"
expect "open: the commit names the run that published the images" 1 "$(git -C "$bare" log -1 --format=%b bump/jobpilot | grep -c '^Images published by https://github.example/owner/jobpilot/actions/runs/1$')"
expect "open: main is untouched" "$main" "$(git -C "$bare" rev-parse main)"

# 2. A lookup GitHub didn't answer properly stops the run before anything moves.
fail_on='GET *pulls?state=open*'
open_fails "open: a failed PR lookup stops the run" "$c4" 2
fail_body=''
open_fails "open: so does a failed lookup with an empty answer" "$c4" 2
normal
answer_on='GET *pulls?state=open*' answer_body='[{}]'
open_fails "open: so does a lookup answering something that is not a PR" "$c4" 2
answer_body=''
open_fails "open: so does an empty answer" "$c4" 2
answer_body='   	 '
open_fails "open: so does a blank answer" "$c4" 2
answer_body='[] []'
open_fails "open: so does an answer of two JSON documents" "$c4" 2
normal
expect "and nothing moved" "0 0 $c3" "$(calls PATCH) $(calls POST) $(proposed)"
described "and the PR still says what the branch proposes"

# 3. The branch moves but describing the PR fails; the reruns tidy the PR.
fail_on='PATCH *'
open_fails "open: a failed PR update after the push stops the run" "$c4" 3
normal
expect "the branch has moved" "$c4" "$(proposed)"
answer_on='PATCH *' answer_body='{}'
open_fails "open: an update answered without the PR's address stops the rerun" "$c4" 33
normal
opens "a rerun of the same commit" "$c4" 33
expect "open: the rerun only describes the PR again" "1 0 $c4" "$(calls PATCH) $(calls POST) $(proposed)"
described "open: the PR says what the branch proposes again"
expect "open: with the run that published the branch's images" 1 "$(jq -r .body "$work/state/pr.json" | grep -Fc '[this CI run](https://github.example/owner/jobpilot/actions/runs/3)')"

# 4. Someone pushes a newer proposal after this run looked at the branch: no overwriting it, and
# the next run describes it.
rm -rf "$work/hand"
on_user="git clone -q -b bump/jobpilot '$bare' '$work/hand' && sh '$bump' rewrite '$work/hand/clusters/local/jobpilot.yaml' $c6 $(digest 51) $(digest 52) $(digest 53) && git -C '$work/hand' -c user.name=test -c user.email=test@example.com -c commit.gpgsign=false commit -qam 'By hand' -m 'Images published by https://github.example/someone/else/actions/runs/5' && git -C '$work/hand' push -q origin HEAD:bump/jobpilot"
open_fails "open: a branch moved since this run looked at it stops the push" "$c7" 4
normal
expect "and the other push stays" "$(git -C "$work/hand" rev-parse HEAD)" "$(git -C "$bare" rev-parse bump/jobpilot)"
opens "a later run deploying nothing beyond the branch" "$c5" 5
expect "open: it describes the PR again from the branch" "1 0 $c6" "$(calls PATCH) $(calls POST) $(proposed)"
described "open: the PR says what the other push proposes"
expect "open: without a run from another repository" "0 1" "$(jq -r .body "$work/state/pr.json" | grep -c 'this CI run') $(jq -r .body "$work/state/pr.json" | grep -c "the commit's page links the run")"

# 5. An older commit, and one deploying nothing new, leave a well-described PR alone.
opens "an older commit" "$c3" 6
expect "open: an older commit leaves the PR alone" "0 0 $c6" "$(calls PATCH) $(calls POST) $(proposed)"
opens "a commit deploying nothing" "$c2" 6
expect "open: so does a commit deploying nothing since main's, after one lookup" "0 0 1" "$(calls PATCH) $(calls POST) $(calls GET)"

# 6. A newer deployed change moves the PR, and titles keep shell syntax as written.
opens "a newer deployed change" "$c7" 7
expect "open: it moves the PR" "1 0 $c7" "$(calls PATCH) $(calls POST) $(proposed)"
expect "open: the branch is rebuilt from main, not stacked" 1 "$(git -C "$bare" rev-list --count main..bump/jobpilot)"
described "open: the moved PR says what the branch proposes"
expect "open: the PR body keeps a title exactly" 1 "$(jq -r .body "$work/state/pr.json" | grep -Fxc -- "- owner/jobpilot#8 $nasty")"
expect "open: so does the commit message" 1 "$(git -C "$bare" log -1 --format=%b bump/jobpilot | grep -Fxc -- "- owner/jobpilot#8 $nasty")"
expect "open: main is still untouched" "$main" "$(git -C "$bare" rev-parse main)"

# 7. main catches up by another route, as a bump by hand would. A push to the branch while the PR
# is being closed reopens it; once main runs the proposal or something newer, the PR is closed.
main_runs() { # COMMIT RUN
  sh "$bump" rewrite "$work/seed/clusters/local/jobpilot.yaml" "$1" "$(digest "${2}1")" "$(digest "${2}2")" "$(digest "${2}3")"
  git -C "$work/seed" -c user.name=test -c user.email=test@example.com -c commit.gpgsign=false commit -qam "By hand: the lab runs $(short "$1")"
  git -C "$work/seed" push -q origin HEAD:main
}
main_runs "$c7" 7
rm -rf "$work/hand2"
on_patch="[ -e '$work/hand2' ] || { git clone -q -b bump/jobpilot '$bare' '$work/hand2' && sh '$bump' rewrite '$work/hand2/clusters/local/jobpilot.yaml' $c8 $(digest 81) $(digest 82) $(digest 83) && git -C '$work/hand2' -c user.name=test -c user.email=test@example.com -c commit.gpgsign=false commit -qam 'By hand' -m 'Images published by https://github.example/owner/jobpilot/actions/runs/5x' && git -C '$work/hand2' push -q origin HEAD:bump/jobpilot; }"
open_fails "open: a push while the PR is being closed stops the run" "$c7" 8
normal
expect "and the PR is open again, for the newer proposal" "open $c8" "$(pr_state) $(proposed)"
opens "the run after it" "$c7" 8
described "open: the run after it describes the newer proposal"
expect "open: without a run whose id is not a number" "0 1" "$(jq -r .body "$work/state/pr.json" | grep -c 'this CI run') $(jq -r .body "$work/state/pr.json" | grep -c "the commit's page links the run")"
expect "open: the PR body keeps a commit subject exactly" 1 "$(jq -r .body "$work/state/pr.json" | grep -Fxc -- "- $plain ($(short "$c8"))")"
main_runs "$c9" 9
opens "a run after main got ahead of the PR" "$c9" 9
expect "open: it closes the PR" "none closed" "$(pr_state) $(jq -r .state "$work/state/closed.json")"
expect "open: and says why" 1 "$(jq -r .body "$work/state/closed.json" | grep -c '^Closed by jobpilot')"

# 8. With no open PR, a creation that fails after the push is redone by the next run.
fail_on='POST *'
open_fails "open: a failed PR creation stops the run" "$c10" 10
normal
expect "after the push" "none $c10" "$(pr_state) $(proposed)"
opens "the next run" "$c10" 10
expect "open: the next run opens the PR" "1 open" "$(calls POST) $(pr_state)"
expect "open: the branch is one commit on the new main" 1 "$(git -C "$bare" rev-list --count main..bump/jobpilot)"
described "open: the new PR says what the branch proposes"

rm -f "$work/api/calls"
# shellcheck disable=SC2016 # the inner sh expands $1..$3
refuses "open refuses to run without a token" env PATH="$work/bin:$PATH" STUB="$work/api" STUB_STATE="$work/state" \
  STUB_FAIL='' STUB_FAIL_BODY='' STUB_ANSWER='' STUB_ANSWER_BODY='' STUB_ON_USER='' STUB_ON_PATCH='' GITHUB_API_URL=https://api.github.example \
  SHA="$c8" API_DIGEST="$(digest 61)" WEB_DIGEST="$(digest 62)" MIGRATE_DIGEST="$(digest 63)" \
  GH_TOKEN='' APP_SLUG=jobpilot-bump INFRA_REPO=owner/infra JOBPILOT_REPO=owner/jobpilot RUN_URL=https://github.example/run \
  sh -c 'cd "$1" && sh "$2" open "$3"' sh "$repo" "$bump" "$work/infra"
expect "and says so, before asking GitHub anything" "1 0" "$(grep -c 'GH_TOKEN is not set' "$work/refused.out") $(calls GET)"

if [ "$failures" -ne 0 ]; then
  echo "infra-bump-test: $failures failed"
  exit 1
fi
echo "infra-bump-test: all passed"
