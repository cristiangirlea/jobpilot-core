#!/usr/bin/env bash
# /check's integration step (.claude/skills/check/SKILL.md, step 5) on a stack of its own, so
# several sessions can run it at once: a uniquely named Postgres container on a port Docker picks,
# the API and the fake ATS on free random ports, the API bound to loopback, and a check before
# each test step that the API answering on its port is the binary this run built and started.
# Runs from the repository root in Git Bash or on Linux; stops its own API and container on exit.
#
# Never the owner's stack: nothing here uses 5432 or 8080.
set -euo pipefail
export MSYS_NO_PATHCONV=1 # docker exec/cp paths are container paths, not Git Bash ones

id="check-$(date +%s)-$$-$RANDOM"
run=$(mktemp -d)
pg="jobpilot-$id"
api_pid=
windows=
command -v cygpath > /dev/null 2>&1 && windows=1

# The API binary lives in this run's own temporary directory, so its full path names this run's
# process and no other. On Windows `go build -o` gets a Windows path: with MSYS_NO_PATHCONV set,
# /c/x would be taken literally and land in <drive>:\c\x.
if [ -n "$windows" ]; then
	exe="$run/api.exe"
	exe_path=$(cygpath -w "$exe")
else
	exe="$run/api"
	exe_path=$(readlink -f "$run")/api
fi

cleanup() {
	if [ -n "$windows" ]; then
		# Git Bash's $! is a bash stub, not api.exe: stop the process by its unique path.
		powershell -NoProfile -Command "Get-Process | Where-Object { \$_.Path -eq '$exe_path' } | Stop-Process -Force" 2> /dev/null || true
	fi
	if [ -n "$api_pid" ]; then kill "$api_pid" 2> /dev/null || true; fi
	docker rm -f "$pg" > /dev/null 2>&1 || true
	echo "check-integration: stopped; API log at $run/api.log"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# in_use PORT: true when anything listens on TCP port PORT, on any address.
in_use() {
	if [ -n "$windows" ]; then
		powershell -NoProfile -Command "if (Get-NetTCPConnection -State Listen -LocalPort $1 -ErrorAction SilentlyContinue) { exit 0 } else { exit 1 }"
	else
		ss -Htln "sport = :$1" | grep -q .
	fi
}

# listeners PORT: "<pid> <executable>" for each process listening on PORT, one per line.
listeners() {
	if [ -n "$windows" ]; then
		powershell -NoProfile -Command "Get-NetTCPConnection -State Listen -LocalPort $1 -ErrorAction SilentlyContinue | ForEach-Object OwningProcess | Sort-Object -Unique | ForEach-Object { '{0} {1}' -f \$_, (Get-Process -Id \$_ -ErrorAction SilentlyContinue).Path }"
	else
		local pid
		for pid in $(ss -Htlnp "sport = :$1" | sed -n 's/.*pid=\([0-9]*\).*/\1/p' | sort -u); do
			echo "$pid $(readlink -f "/proc/$pid/exe" 2> /dev/null)"
		done
	fi | tr -d '\r'
}

# free_port: a random port in 20000-39999 that nothing listens on. Another session can still take
# it before this run binds it; the API check below and the fake ATS's own bind error catch that.
free_port() {
	local p
	for _ in $(seq 1 50); do
		p=$((20000 + RANDOM % 20000))
		if ! in_use "$p"; then
			echo "$p"
			return 0
		fi
	done
	echo "check-integration: no free port found" >&2
	return 1
}

api_port=$(free_port)
ats_port=$(free_port)
while [ "$ats_port" = "$api_port" ]; do ats_port=$(free_port); done

echo "check-integration: run $id, API on 127.0.0.1:$api_port, fake ATS on $ats_port"

docker run -d --name "$pg" --label "jobpilot-check=$id" \
	-e POSTGRES_USER=jobpilot -e POSTGRES_PASSWORD=jobpilot -e POSTGRES_DB=jobpilot \
	-p 127.0.0.1::5432 postgres:16-alpine > /dev/null
pg_port=$(docker port "$pg" 5432/tcp | head -n1 | sed 's/.*://' | tr -d '\r')
# During initdb the server listens on its socket only, so a TCP probe waits for the real start.
for _ in $(seq 1 60); do
	docker exec "$pg" pg_isready -q -h 127.0.0.1 -U jobpilot && break
	sleep 1
done
docker exec "$pg" pg_isready -h 127.0.0.1 -U jobpilot
db="postgres://jobpilot:jobpilot@127.0.0.1:$pg_port/jobpilot?sslmode=disable"
echo "check-integration: Postgres $pg on 127.0.0.1:$pg_port"

# migrate.sh with the container's own psql: no local psql and no shared image tag needed.
docker cp migrations "$pg:/tmp/migrations"
docker exec -e "DATABASE_URL=postgres://jobpilot:jobpilot@127.0.0.1:5432/jobpilot?sslmode=disable" \
	"$pg" sh /tmp/migrations/migrate.sh > "$run/migrate.log"
echo "check-integration: migrations applied ($(grep -c '^apply ' "$run/migrate.log"))"

(cd apps/api && JOBPILOT_TEST_DATABASE_URL="$db" go test -count=1 -run Postgres ./...)

(cd apps/api && go build -o "$exe_path" ./cmd/api)

API_ADDR="127.0.0.1:$api_port" \
	DATABASE_URL="$db" \
	JWT_SECRET="throwaway-$id" \
	TOKEN_TTL_HOURS=1 \
	LLM_PROVIDER=none \
	REGISTRATION=open \
	ATS_GREENHOUSE_URL="http://localhost:$ats_port" \
	ATS_ASHBY_URL="http://localhost:$ats_port" \
	ATS_RECRUITEE_URL="http://localhost:$ats_port" \
	OUTBOX_SENDER_ENABLED=true \
	OUTBOX_SEND_DELAY_SECONDS=3 \
	"$exe" > "$run/api.log" 2>&1 &
api_pid=$!

lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

# ours: exactly one process listens on the API port and it runs this run's binary, the log says
# it listens there, and it answers. Another session's API on the port fails this, not the tests.
# (A PID comparison would not do: in Git Bash $! is a bash stub, not api.exe.)
ours() {
	local l
	l=$(listeners "$api_port")
	[ -n "$l" ] && [ "$(printf '%s\n' "$l" | wc -l)" -eq 1 ] &&
		[ "$(lower "${l#* }")" = "$(lower "$exe_path")" ] &&
		grep -q "JobPilot API listening.*addr=127.0.0.1:$api_port" "$run/api.log" &&
		curl -fs "http://127.0.0.1:$api_port/api/health" > /dev/null
}
for _ in $(seq 1 30); do
	ours && break
	if ! kill -0 "$api_pid" 2> /dev/null; then
		cat "$run/api.log"
		echo "check-integration: the API exited (port $api_port taken or reserved? run again)" >&2
		exit 1
	fi
	sleep 1
done
assert_ours() {
	if ! ours; then
		echo "check-integration: 127.0.0.1:$api_port is not served by this run's API ($exe_path); listeners: $(listeners "$api_port" | tr '\n' ';')" >&2
		exit 1
	fi
}
assert_ours
echo "check-integration: $exe_path answers on 127.0.0.1:$api_port"

ps=(pwsh -NoProfile -File)
command -v pwsh > /dev/null 2>&1 || ps=(powershell -NoProfile -ExecutionPolicy Bypass -File)
export JOBPILOT_API_URL="http://127.0.0.1:$api_port/api"
"${ps[@]}" scripts/smoke.ps1
assert_ours
"${ps[@]}" scripts/seniority-filter-smoke.ps1
assert_ours
(cd apps/mcp && JOBPILOT_E2E_ATS_PORT="$ats_port" go test -count=1 -run TestEndToEnd . -args -api "http://127.0.0.1:$api_port")
assert_ours

echo "check-integration: passed"
