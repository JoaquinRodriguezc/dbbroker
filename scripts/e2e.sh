#!/usr/bin/env bash
set -euo pipefail

API="${DBBROKER_API:-http://127.0.0.1:8787}"
suffix="${RANDOM}${RANDOM}"
template="e2e_${suffix}"
repository="/tmp/dbbroker-e2e-repo-${suffix}/.git"
worktree="/tmp/dbbroker-e2e-worktree-${suffix}"
backup="$(mktemp)"

cleanup() {
  curl -sS -X POST -H 'Content-Type: application/json' \
    -d "{\"repository\":\"$repository\",\"worktree\":\"$worktree\"}" "$API/v1/release" >/dev/null 2>&1 || true
  curl -sS -X DELETE -H "X-DBBroker-Repository: $repository" "$API/v1/templates/$template" >/dev/null 2>&1 || true
  rm -f "$backup"
  rmdir "$worktree" >/dev/null 2>&1 || true
}
trap cleanup EXIT

mkdir -p "$worktree"
cat >"$backup" <<'SQL'
CREATE TABLE widgets (id bigint PRIMARY KEY, name text NOT NULL);
INSERT INTO widgets VALUES (1, 'seeded');
SQL

curl --fail-with-body -sS "$API/healthz" >/dev/null
template_database="$(curl --fail-with-body -sS -X POST -H "X-DBBroker-Repository: $repository" \
  --data-binary "@$backup" "$API/v1/templates/$template" | jq -r .database_name)"

response="$(curl --fail-with-body -sS -X POST \
  -H 'Content-Type: application/json' \
  -d "{\"repository\":\"$repository\",\"worktree\":\"$worktree\",\"template\":\"$template\"}" \
  "$API/v1/acquire")"
database="$(jq -r .database_name <<<"$response")"
value="$(docker compose exec -T postgres psql -U dbbroker -d "$database" -Atc 'SELECT name FROM widgets WHERE id=1;')"
[[ "$value" == "seeded" ]] || { echo "unexpected cloned value: $value" >&2; exit 1; }

curl --fail-with-body -sS -X POST -H 'Content-Type: application/json' \
  -d "{\"repository\":\"$repository\",\"worktree\":\"$worktree\"}" "$API/v1/release" >/dev/null

exists="$(docker compose exec -T postgres psql -U dbbroker -d postgres -Atc \
  "SELECT count(*) FROM pg_database WHERE datname='$database';")"
[[ "$exists" == "0" ]] || { echo "database was not dropped" >&2; exit 1; }

curl --fail-with-body -sS -X DELETE -H "X-DBBroker-Repository: $repository" "$API/v1/templates/$template" >/dev/null
exists="$(docker compose exec -T postgres psql -U dbbroker -d postgres -Atc \
  "SELECT count(*) FROM pg_database WHERE datname='$template_database';")"
[[ "$exists" == "0" ]] || { echo "template was not dropped" >&2; exit 1; }
status="$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE -H "X-DBBroker-Repository: $repository" "$API/v1/templates/$template")"
[[ "$status" == "404" ]] || { echo "second unseed returned $status, expected 404" >&2; exit 1; }

echo "e2e ok: seed → acquire → query → release → unseed"
