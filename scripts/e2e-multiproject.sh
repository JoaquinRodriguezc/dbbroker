#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI="$ROOT/scripts/dbbroker"
suffix="${RANDOM}${RANDOM}"
repo_a="/tmp/dbbroker-repo-a-$suffix"
repo_b="/tmp/dbbroker-repo-b-$suffix"
template="main_$suffix"

cleanup() {
  for repo in "$repo_a" "$repo_b"; do
    if [[ -d "$repo/.git" ]]; then (cd "$repo" && "$CLI" release >/dev/null 2>&1 || true); fi
    rm -rf "$repo"
  done
}
trap cleanup EXIT

git init -q "$repo_a"
git init -q "$repo_b"
printf '%s\n' 'CREATE TABLE project_marker (value text);' "INSERT INTO project_marker VALUES ('A');" >"$repo_a/backup.sql"
printf '%s\n' 'CREATE TABLE project_marker (value text);' "INSERT INTO project_marker VALUES ('B');" >"$repo_b/backup.sql"

(cd "$repo_a" && "$CLI" seed "$template" backup.sql >/dev/null && "$CLI" acquire "$template" --env-file .env.local >/dev/null)
(cd "$repo_b" && "$CLI" seed "$template" backup.sql >/dev/null && "$CLI" acquire "$template" --env-file .env.local >/dev/null)

db_a="$(sed -nE 's#^DATABASE_URL=.*/([^/?]+).*#\1#p' "$repo_a/.env.local")"
db_b="$(sed -nE 's#^DATABASE_URL=.*/([^/?]+).*#\1#p' "$repo_b/.env.local")"
[[ -n "$db_a" && -n "$db_b" && "$db_a" != "$db_b" ]] || { echo "repositories did not receive distinct databases" >&2; exit 1; }

value_a="$(docker compose -f "$ROOT/docker-compose.yml" exec -T postgres psql -U dbbroker -d "$db_a" -Atc 'SELECT value FROM project_marker;')"
value_b="$(docker compose -f "$ROOT/docker-compose.yml" exec -T postgres psql -U dbbroker -d "$db_b" -Atc 'SELECT value FROM project_marker;')"
[[ "$value_a" == A && "$value_b" == B ]] || { echo "template data crossed repository boundary: $value_a/$value_b" >&2; exit 1; }

echo "multi-project e2e ok: same template name, isolated data"
