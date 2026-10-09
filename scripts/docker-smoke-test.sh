#!/usr/bin/env bash
# Smoke test for the AstrLink server image, shared by CI and local runs:
#
#   scripts/docker-smoke-test.sh [image]    (default: astrlink:local)
#
# It needs docker and curl, and leaves no containers, volumes or files behind.
set -euo pipefail

image=${1:-astrlink:local}
prefix="astrlink-smoke-$$"
password="smoke test password"
work=$(mktemp -d)
bind_dir=$(mktemp -d)
container=""

cleanup() {
	docker rm -f "$prefix-volume" "$prefix-bind" >/dev/null 2>&1 || true
	docker volume rm "$prefix-data" >/dev/null 2>&1 || true
	# Core's files in the bind mount belong to its uid; remove them as root
	# and hand the directory back so it can be removed.
	docker run --rm --entrypoint sh -v "$bind_dir:/mnt" "$image" \
		-c "find /mnt -mindepth 1 -delete; chown $(id -u):$(id -g) /mnt" >/dev/null 2>&1 || true
	rm -rf "$work" "$bind_dir"
}
trap cleanup EXIT

fail() {
	echo "FAIL: $*" >&2
	if [ -n "$container" ]; then
		docker logs --tail 40 "$container" >&2 || true
	fi
	exit 1
}

pass() {
	echo "ok   $*"
}

wait_healthy() {
	local health=""
	for _ in $(seq 1 120); do
		health=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$container")
		case $health in
		healthy) return ;;
		unhealthy) fail "$container became unhealthy" ;;
		esac
		if [ "$(docker inspect -f '{{.State.Running}}' "$container")" != true ]; then
			fail "$container stopped"
		fi
		sleep 1
	done
	fail "$container was not healthy after 120 seconds (last: ${health:-none})"
}

base_url() {
	local port
	port=$(docker port "$container" 8317/tcp | head -n 1)
	echo "http://127.0.0.1:${port##*:}"
}

# request METHOD PATH [JSON] [EXTRA CURL ARGS...] prints the status code and
# leaves the body in $work/body. Every request uses the session cookie jar;
# writes carry the console header unless console_header=0.
request() {
	local method=$1 path=$2 body=${3:-}
	shift $(($# < 3 ? $# : 3))
	local args=(-sS -o "$work/body" -w '%{http_code}' -X "$method" -b "$work/cookies" -c "$work/cookies")
	if [ "$method" != GET ] && [ "${console_header:-1}" = 1 ]; then
		args+=(-H "X-AstrLink-Console: 1")
	fi
	if [ -n "$body" ]; then
		args+=(-H "Content-Type: application/json" --data "$body")
	fi
	curl "${args[@]}" "$@" "$url$path"
}

expect() {
	local want=$1 got=$2 what=$3
	if [ "$got" != "$want" ]; then
		fail "$what: HTTP $got, want $want: $(cat "$work/body" 2>/dev/null)"
	fi
	pass "$what"
}

# expect_authenticated CODE: the gateway accepted the access token. With no
# provider configured it answers with its own error, never 401.
expect_authenticated() {
	local got=$1 what=$2
	if [ "$got" = 401 ] || [ "$got" = 000 ]; then
		fail "$what: HTTP $got: $(cat "$work/body" 2>/dev/null)"
	fi
	pass "$what (HTTP $got)"
}

# json_field NAME reads a string field from the compact JSON in $work/body.
json_field() {
	sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p" "$work/body"
}

expect_console_status() {
	local want=$1
	expect 200 "$(request GET /console/v1/status)" "console status"
	if [ "$(json_field status)" != "$want" ]; then
		fail "console status is $(json_field status), want $want"
	fi
	pass "console status is $want"
}

login() {
	expect 200 "$(request POST /console/v1/login "{\"password\":\"$password\"}")" "sign in with the raw password"
}

core_uid() {
	# shellcheck disable=SC2016 # expanded by the container's shell
	docker exec "$container" sh -c 'for d in /proc/[0-9]*; do
		if [ "$(cat "$d/comm" 2>/dev/null)" = astrlink-core ]; then
			sed -n "s/^Uid:[[:space:]]*\([0-9]*\).*/\1/p" "$d/status"
		fi
	done'
}

echo "== named volume ($image)"
container="$prefix-volume"
docker run -d --name "$container" -v "$prefix-data:/data" -p 127.0.0.1::8317 "$image" >/dev/null
wait_healthy
pass "container is healthy"
url=$(base_url)
uid=$(core_uid)
[ "$uid" = 10001 ] || fail "Core runs as uid '${uid}', want 10001"
pass "Core runs as uid 10001, not root"

expect_console_status setup_required
expect 403 "$(console_header=0 request POST /console/v1/setup "{\"password\":\"$password\"}")" \
	"setup without the console header is refused"
rm -f "$work/cookies"
expect 200 "$(request POST /console/v1/setup "{\"password\":\"$password\"}")" "first-run setup"
grep -q astrlink_session "$work/cookies" || fail "setup set no session cookie"
pass "setup started a session"
expect 204 "$(request POST /console/v1/logout)" "sign out"
expect 401 "$(request POST /console/v1/login '{"password":"wrong password"}')" "a wrong password is refused"
login
expect 201 "$(request POST /control/v1/access-tokens '{"name":"smoke test"}')" "create an access token"
token=$(json_field access_token)
[ -n "$token" ] || fail "no access token in $(cat "$work/body")"
expect 401 "$(request GET /v1/models)" "/v1/models without an access token is refused, session cookie or not"
expect_authenticated "$(request GET /v1/models "" -H "Authorization: Bearer $token")" "/v1/models accepts the access token"

docker exec "$container" astrlink --help >/dev/null || fail "docker exec astrlink --help"
pass "docker exec astrlink --help"
docker exec "$container" astrlink requests --limit 1 >/dev/null || fail "docker exec astrlink requests (control socket)"
pass "docker exec astrlink reaches Core through the control socket"
owner=$(docker exec "$container" stat -c %U /data/astrlink.db)
[ "$owner" = astrlink ] || fail "/data/astrlink.db is owned by $owner"
pass "data files belong to the astrlink user"

docker restart "$container" >/dev/null
wait_healthy
pass "healthy again after a restart"
# The ephemeral host port changes on restart.
url=$(base_url)
rm -f "$work/cookies"
expect_console_status login_required
login
expect_authenticated "$(request GET /v1/models "" -H "Authorization: Bearer $token")" "the access token survived the restart"

started=$(date +%s)
docker stop "$container" >/dev/null
elapsed=$(($(date +%s) - started))
code=$(docker inspect -f '{{.State.ExitCode}}' "$container")
[ "$code" = 0 ] || fail "Core exited with $code after docker stop"
[ "$elapsed" -lt 10 ] || fail "docker stop took ${elapsed}s; SIGTERM did not stop Core"
pass "docker stop: SIGTERM reached Core, which exited cleanly in ${elapsed}s"

echo "== bind mount of a root-owned host directory"
docker run --rm --entrypoint sh -v "$bind_dir:/mnt" "$image" -c 'chown 0:0 /mnt && chmod 0755 /mnt'
container="$prefix-bind"
docker run -d --name "$container" -v "$bind_dir:/data" -p 127.0.0.1::8317 "$image" >/dev/null
wait_healthy
pass "container is healthy"
url=$(base_url)
rm -f "$work/cookies"
expect_console_status setup_required
expect 200 "$(request POST /console/v1/setup "{\"password\":\"$password\"}")" "first-run setup"
# Looked at through a second container, so host permissions do not matter.
docker run --rm --entrypoint test -v "$bind_dir:/mnt" "$image" -f /mnt/astrlink.db ||
	fail "no database in the bind-mounted directory"
pass "the database is in the host directory"

echo "all smoke tests passed"
