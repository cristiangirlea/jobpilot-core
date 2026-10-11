#!/bin/sh
# Tests scripts/infra-bump.sh: the file rewrite and what it refuses, the bump decision, the change
# list, and `open` end to end, against a local bare repository standing in for infra and a stub curl
# standing in for GitHub's API. Run with sh from anywhere (CI uses dash); needs git and jq.
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

# --- pinned and rewrite, on copies of infra's clusters/local/jobpilot.yaml

expect "pinned reads the commit" "$fixture_commit" "$(sh "$bump" pinned "$fixture")"

cp "$fixture" "$work/a.yaml"
sh "$bump" rewrite "$work/a.yaml" "$(hex40 1)" "$(digest 2)" "$(digest 3)" "$(digest 4)"
expect "rewrite pins the commit" "$(hex40 1)" "$(sh "$bump" pinned "$work/a.yaml")"
expect "rewrite pins the API image" "$(digest 2)" "$(image "$work/a.yaml" jobpilot-api)"
expect "rewrite pins the web image" "$(digest 3)" "$(image "$work/a.yaml" jobpilot-web)"
expect "rewrite pins the migrate image" "$(digest 4)" "$(image "$work/a.yaml" jobpilot-migrate)"
expect "rewrite changes those four lines and nothing else" 4 "$(diff "$fixture" "$work/a.yaml" | grep -c '^>')"

cp "$fixture" "$work/b.yaml"
refuses "rewrite refuses a short commit" sh "$bump" rewrite "$work/b.yaml" 8bd9629 "$(digest 2)" "$(digest 3)" "$(digest 4)"
refuses "rewrite refuses an uppercase commit" sh "$bump" rewrite "$work/b.yaml" "$(hex40 1 | tr 0 A)" "$(digest 2)" "$(digest 3)" "$(digest 4)"
refuses "rewrite refuses a digest without sha256:" sh "$bump" rewrite "$work/b.yaml" "$(hex40 1)" "$(printf '%064d' 2)" "$(digest 3)" "$(digest 4)"
refuses "rewrite refuses a short digest" sh "$bump" rewrite "$work/b.yaml" "$(hex40 1)" "$(digest 2)" sha256:abc "$(digest 4)"
refuses "rewrite refuses a digest carrying a second line" sh "$bump" rewrite "$work/b.yaml" "$(hex40 1)" "$(digest 2)" "$(digest 3)" "$(digest 4)
      digest: $(digest 9)"
refuses "rewrite refuses a missing argument" sh "$bump" rewrite "$work/b.yaml" "$(hex40 1)" "$(digest 2)" "$(digest 3)"
unchanged "a refused rewrite leaves the file as it was" "$fixture" "$work/b.yaml"

grep -v 'name: jobpilot-migrate$' "$fixture" > "$work/c.yaml"
cp "$work/c.yaml" "$work/c.orig"
refuses "rewrite refuses a file without one of the images" sh "$bump" rewrite "$work/c.yaml" "$(hex40 1)" "$(digest 2)" "$(digest 3)" "$(digest 4)"
unchanged "and leaves it as it was" "$work/c.orig" "$work/c.yaml"

awk '{ print } /- name: jobpilot-api$/ { f = 1 } f && /digest:/ { print "    - name: jobpilot-api"; print; f = 0 }' "$fixture" > "$work/d.yaml"
refuses "rewrite refuses an image listed twice" sh "$bump" rewrite "$work/d.yaml" "$(hex40 1)" "$(digest 2)" "$(digest 3)" "$(digest 4)"

awk '{ print } /^ *commit: / { print }' "$fixture" > "$work/e.yaml"
refuses "pinned refuses two pinned commits" sh "$bump" pinned "$work/e.yaml"
refuses "rewrite refuses two pinned commits" sh "$bump" rewrite "$work/e.yaml" "$(hex40 1)" "$(digest 2)" "$(digest 3)" "$(digest 4)"

# --- decide and changes, on a small history shaped like jobpilot's

repo="$work/jobpilot"
git init -q -b master "$repo"
g() { git -C "$repo" -c user.name=test -c user.email=test@example.com -c commit.gpgsign=false "$@"; }
commit_file() { # PATH SUBJECT: prints the new commit
  mkdir -p "$repo/$(dirname "$1")"
  echo "$2" >> "$repo/$1"
  g add -A
  g commit -q -m "$2"
  g rev-parse HEAD
}
c1=$(commit_file apps/api/main.go "Start")
c2=$(commit_file docs/notes.md "Docs only")
g checkout -q -b web
commit_file apps/web/page.tsx "Web change" > /dev/null
g checkout -q master
g merge -q --no-ff web -m "Merge pull request #7 from owner/web" -m "Show the web change"
c3=$(g rev-parse HEAD)
c4=$(commit_file deploy/base/api.yaml "Deploy change")
c5=$(commit_file apps/mcp/main.go "MCP only")
c6=$(commit_file migrations/019_example.sql "A migration")

decide() { (cd "$repo" && sh "$bump" decide "$1" "$2"); }
expect "decide: a docs-only commit is the same" same "$(decide "$c1" "$c2")"
expect "decide: a merged web change is a bump" bump "$(decide "$c1" "$c3")"
expect "decide: a deploy/ change is a bump" bump "$(decide "$c3" "$c4")"
expect "decide: apps/mcp is not deployed" same "$(decide "$c4" "$c5")"
expect "decide: a migration is a bump" bump "$(decide "$c5" "$c6")"
expect "decide: the running commit itself is the same" same "$(decide "$c3" "$c3")"
expect "decide: an ancestor of the running commit is older" older "$(decide "$c4" "$c1")"
expect "decide: a running commit this history lacks is a bump" bump "$(decide "$(hex40 7)" "$c1")"

expect "changes lists merged PRs by number and title, other commits by subject" "- Docs only ($(short "$c2"))
- owner/jobpilot#7 Show the web change
- Deploy change ($(short "$c4"))" "$(cd "$repo" && sh "$bump" changes "$c1" "$c4" owner/jobpilot)"

# --- open, end to end: infra's main pins c1

bare="$work/infra.git"
git init -q --bare -b main "$bare"
git clone -q "$bare" "$work/seed" 2> /dev/null
mkdir -p "$work/seed/clusters/local"
sed "s/$fixture_commit/$c1/" "$fixture" > "$work/seed/clusters/local/jobpilot.yaml"
git -C "$work/seed" add -A
git -C "$work/seed" -c user.name=test -c user.email=test@example.com -c commit.gpgsign=false commit -q -m "JobPilot at the start"
git -C "$work/seed" push -q origin HEAD:main
main=$(git -C "$bare" rev-parse main)

# The stub API logs each call as "METHOD URL" in $STUB/calls, keeps the JSON it was sent in
# $STUB/data.<call number>, and answers the open-PR lookup from $STUB/open-prs.json.
mkdir -p "$work/bin"
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
case "$method $url" in
  "GET "*/users/jobpilot-bump%5Bbot%5D) echo '{"login": "jobpilot-bump[bot]", "id": 42}' ;;
  "GET "*/repos/owner/infra/pulls\?*) cat "$STUB/open-prs.json" ;;
  "POST "*/repos/owner/infra/pulls) echo '{"number": 9, "html_url": "https://github.example/owner/infra/pull/9"}' ;;
  "PATCH "*/repos/owner/infra/pulls/9) echo '{"number": 9, "html_url": "https://github.example/owner/infra/pull/9"}' ;;
  *) echo "stub curl: unexpected $method $url" >&2; exit 22 ;;
esac
EOF
chmod +x "$work/bin/curl"

open() { # SHA DIGEST-SEED [OPEN-PRS-JSON]: a fresh checkout of infra's main, as each CI run has
  rm -rf "$work/infra" "$work/api"
  mkdir -p "$work/api"
  printf '%s' "${3:-[]}" > "$work/api/open-prs.json"
  git clone -q "$bare" "$work/infra" 2> /dev/null
  (cd "$repo" && PATH="$work/bin:$PATH" STUB="$work/api" GITHUB_API_URL=https://api.github.example \
    SHA=$1 API_DIGEST="$(digest "${2}1")" WEB_DIGEST="$(digest "${2}2")" MIGRATE_DIGEST="$(digest "${2}3")" \
    GH_TOKEN=test-token APP_SLUG=jobpilot-bump INFRA_REPO=owner/infra JOBPILOT_REPO=owner/jobpilot \
    RUN_URL=https://github.example/owner/jobpilot/actions/runs/1 sh "$bump" open "$work/infra")
}
calls() { # METHOD: how many such calls the last run made
  if [ -f "$work/api/calls" ]; then grep -c "^$1 " "$work/api/calls" || true; else echo 0; fi
}
proposed() { # the commit the bump branch pins
  git -C "$bare" show bump/jobpilot:clusters/local/jobpilot.yaml > "$work/branch.yaml"
  sh "$bump" pinned "$work/branch.yaml"
}

open "$c3" 1 > "$work/out" 2>&1 || { cat "$work/out"; fail "open with no PR yet failed"; }
expect "open: a first bump opens a PR" 1 "$(calls POST)"
expect "open: it looks the PR up by branch and base" 1 "$(grep -c '/repos/owner/infra/pulls?state=open&base=main&head=owner:bump/jobpilot$' "$work/api/calls")"
post=$(grep -n '^POST ' "$work/api/calls" | cut -d: -f1)
expect "open: the PR goes from bump/jobpilot to main" "bump/jobpilot main" "$(jq -r '.head + " " + .base' "$work/api/data.$post")"
expect "open: the PR names the commit" "JobPilot: run $(short "$c3") on the lab" "$(jq -r .title "$work/api/data.$post")"
expect "open: the PR lists the jobpilot PRs it carries" 1 "$(jq -r .body "$work/api/data.$post" | grep -c '^- owner/jobpilot#7 Show the web change$')"
expect "open: the PR links the CI run" 1 "$(jq -r .body "$work/api/data.$post" | grep -c 'actions/runs/1)')"
expect "open: the branch pins the commit" "$c3" "$(proposed)"
expect "open: the branch pins its images" "$(digest 11)" "$(image "$work/branch.yaml" jobpilot-api)"
expect "open: the branch is one commit on main" 1 "$(git -C "$bare" rev-list --count main..bump/jobpilot)"
expect "open: the commit is the App's bot's" "jobpilot-bump[bot] <42+jobpilot-bump[bot]@users.noreply.github.com>" "$(git -C "$bare" log -1 --format='%an <%ae>' bump/jobpilot)"
expect "open: main is untouched" "$main" "$(git -C "$bare" rev-parse main)"

open "$c4" 2 '[{"number": 9}]' > "$work/out" 2>&1 || { cat "$work/out"; fail "open with a PR failed"; }
expect "open: a newer deployed change moves the open PR" "1 0" "$(calls PATCH) $(calls POST)"
expect "open: the branch pins the newer commit" "$c4" "$(proposed)"
expect "open: the branch is rebuilt from main, not stacked" 1 "$(git -C "$bare" rev-list --count main..bump/jobpilot)"
expect "open: main is still untouched" "$main" "$(git -C "$bare" rev-parse main)"

open "$c3" 3 '[{"number": 9}]' > "$work/out" 2>&1 || { cat "$work/out"; fail "open with an older commit failed"; }
expect "open: an older commit leaves the PR alone" "0 0 $c4" "$(calls PATCH) $(calls POST) $(proposed)"

open "$c5" 4 '[{"number": 9}]' > "$work/out" 2>&1 || { cat "$work/out"; fail "open with nothing new deployed failed"; }
expect "open: a commit deploying nothing beyond the PR leaves it alone" "0 0 $c4" "$(calls PATCH) $(calls POST) $(proposed)"

open "$c2" 5 > "$work/out" 2>&1 || { cat "$work/out"; fail "open with nothing deployed failed"; }
expect "open: nothing deployed since main's commit asks GitHub nothing" "0 0" "$(calls GET) $(calls POST)"

rm -f "$work/api/calls"
# shellcheck disable=SC2016 # the inner sh expands $1..$3
refuses "open refuses to run without a token" env PATH="$work/bin:$PATH" STUB="$work/api" GITHUB_API_URL=https://api.github.example \
  SHA="$c6" API_DIGEST="$(digest 61)" WEB_DIGEST="$(digest 62)" MIGRATE_DIGEST="$(digest 63)" \
  GH_TOKEN= APP_SLUG=jobpilot-bump INFRA_REPO=owner/infra JOBPILOT_REPO=owner/jobpilot RUN_URL=https://github.example/run \
  sh -c 'cd "$1" && sh "$2" open "$3"' sh "$repo" "$bump" "$work/infra"
expect "and says so, before asking GitHub anything" "1 0" "$(grep -c 'GH_TOKEN is not set' "$work/refused.out") $(calls GET)"

if [ "$failures" -ne 0 ]; then
  echo "infra-bump-test: $failures failed"
  exit 1
fi
echo "infra-bump-test: all passed"
