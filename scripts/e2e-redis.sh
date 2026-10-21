#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI="$ROOT/scripts/dbbroker"
COMPOSE=(docker compose -f "$ROOT/docker-compose.yml")
suffix="${RANDOM}${RANDOM}"
repo="/tmp/dbbroker-redis-repo-$suffix"
worktree="/tmp/dbbroker-redis-worktree-$suffix"
template="redis_$suffix"

cleanup() {
  for dir in "$worktree" "$repo"; do
    if [[ -d "$dir" ]]; then (cd "$dir" && "$CLI" release >/dev/null 2>&1 || true); fi
  done
  rm -rf "$worktree" "$repo"
}
trap cleanup EXIT

fail() { echo "e2e-redis: $*" >&2; exit 1; }
env_value() { sed -n "s#^$2=##p" "$1/.env.local"; }
# as DIR CMD... runs redis-cli inside the container with DIR's assigned credentials.
as() {
  local url user pass
  url="$(env_value "$1" REDIS_URL)"; shift
  user="$(sed -E 's#^redis://([^:]+):.*#\1#' <<<"$url")"
  pass="$(sed -E 's#^redis://[^:]+:([^@]+)@.*#\1#' <<<"$url")"
  "${COMPOSE[@]}" exec -T redis redis-cli --no-auth-warning --user "$user" --pass "$pass" "$@" 2>&1 || true
}
admin() { "${COMPOSE[@]}" exec -T redis redis-cli --no-auth-warning -a dbbroker "$@"; }
expect() { [[ "$2" == "$3" ]] || fail "$1: expected '$3', got '$2'"; }
expect_denied() { [[ "$2" == NOPERM* ]] || fail "$1: expected NOPERM, got '$2'"; }

git init -q "$repo"
git -C "$repo" -c user.name=e2e -c user.email=e2e@localhost commit -q --allow-empty -m init
git -C "$repo" worktree add -q "$worktree"
printf '%s\n' 'CREATE TABLE marker (value text);' >"$repo/backup.sql"

(cd "$repo" && "$CLI" seed "$template" backup.sql >/dev/null && "$CLI" acquire "$template" --env-file .env.local >/dev/null)
(cd "$worktree" && "$CLI" acquire "$template" --env-file .env.local >/dev/null)

prefix_a="$(env_value "$repo" REDIS_KEY_PREFIX)"
prefix_b="$(env_value "$worktree" REDIS_KEY_PREFIX)"
[[ -n "$prefix_a" && -n "$prefix_b" && "$prefix_a" != "$prefix_b" ]] || fail "worktrees did not receive distinct prefixes"

expect "own write" "$(as "$repo" SET "${prefix_a}k" A)" OK
expect "own read" "$(as "$repo" GET "${prefix_a}k")" A
expect "client handshake" "$(as "$repo" CLIENT SETINFO LIB-NAME e2e)" OK
expect "info" "$(as "$repo" INFO server | grep -c ^redis_version:)" 1
expect "own channel" "$(as "$repo" PUBLISH "${prefix_a}events" hi)" 0
expect_denied "foreign key" "$(as "$repo" GET "${prefix_b}k")"
expect_denied "unprefixed key" "$(as "$repo" SET bare v)"
expect_denied "foreign channel" "$(as "$repo" PUBLISH "${prefix_b}events" hi)"
expect_denied "flushall" "$(as "$repo" FLUSHALL)"
expect_denied "admin command" "$(as "$repo" CONFIG GET maxmemory)"
expect "worktree write" "$(as "$worktree" SET "${prefix_b}k" B)" OK
expect_denied "worktree isolation" "$(as "$worktree" GET "${prefix_a}k")"

before="$(env_value "$repo" REDIS_URL)"
(cd "$repo" && "$CLI" acquire "$template" --env-file .env.local >/dev/null)
[[ "$(env_value "$repo" REDIS_URL)" == "$before" ]] || fail "re-acquire changed REDIS_URL"

# ACL users live in memory: a heartbeat must restore them after a restart.
"${COMPOSE[@]}" restart redis >/dev/null 2>&1
until [[ "$(admin PING 2>/dev/null)" == PONG ]]; do sleep 0.5; done
(cd "$repo" && "$CLI" heartbeat)
expect "after restart" "$(as "$repo" GET "${prefix_a}k")" A

(cd "$repo" && "$CLI" release)
user_a="${prefix_a%:}"
[[ -z "$(admin ACL GETUSER "$user_a")" ]] || fail "ACL user survived release"
[[ "$(admin EXISTS "${prefix_a}k")" == 0 ]] || fail "keys survived release"
[[ "$(admin GET "${prefix_b}k")" == B ]] || fail "release touched another worktree"

echo "redis e2e ok: per-worktree ACL, isolation, restart recovery, release cleanup"
