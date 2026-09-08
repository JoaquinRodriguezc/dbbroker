# dbbroker

Local HTTP service that gives each Git worktree its own disposable PostgreSQL database.

## Architecture

- One PostgreSQL server stores template databases and worktree databases.
- The broker exposes an HTTP API on `127.0.0.1:8787`.
- Broker metadata lives in the `dbbroker` schema of the administrative `postgres` database.
- A lease is refreshed with heartbeats. The collector drops expired databases.

## Quick start

Requirements: Docker Compose, `curl`, `jq`, and Git.

```bash
cp .env.example .env
docker compose up -d --build
curl http://127.0.0.1:8787/healthz
```

Run the complete smoke test after startup:

```bash
make e2e
make e2e-multiproject
```

Install the helper somewhere on `PATH`:

```bash
install -m 0755 scripts/dbbroker ~/.local/bin/dbbroker
```

From the target Git repository, seed a template from either a custom-format
`pg_dump` or plain SQL backup:

```bash
cd /path/to/project
dbbroker seed main ./backup.dump
```

Template names are scoped to the repository, so every project may safely use
`main` without colliding with another project's template.

Inside any worktree, write the assigned connection to the application's environment file:

```bash
dbbroker acquire main --env-file .env.local
```

The command preserves unrelated entries and replaces a previous `DATABASE_URL`
atomically. Add the chosen environment file to the consuming repository's
`.gitignore`. For tools that require an exported variable instead, `--shell`
remains available.

When work finishes:

```bash
dbbroker release
```

For long-running work, refresh the lease periodically:

```bash
while sleep 60; do dbbroker heartbeat; done &
```

If an agent crashes, the collector removes its database after `DBBROKER_LEASE_TTL`.

For immediate worktree-aware cleanup, keep the host watcher running:

```bash
dbbroker watch 30
```

Every cycle it lists active assignments. Existing absolute worktree paths get a
heartbeat; missing paths are released immediately. A sample systemd user unit is
provided at `systemd/dbbroker-watcher.service`.

## API

Authentication is disabled by default because the API only binds to `127.0.0.1`. The server still supports an optional `DBBROKER_TOKEN` if this policy changes later.

| Method | Route | Purpose |
|---|---|---|
| `POST` | `/v1/templates/{name}` | Stream a backup; requires `X-DBBroker-Repository` |
| `POST` | `/v1/acquire` | Allocate or reuse a database for a worktree |
| `POST` | `/v1/heartbeat` | Refresh its lease |
| `POST` | `/v1/release` | Drop it immediately |
| `GET` | `/v1/assignments` | List active assignments |

Acquire body:

```json
{"repository":"/repo/.git","worktree":"/repo-feature","template":"main"}
```

## Implementation steps

1. **Bootstrap:** Compose, configuration, health endpoint and metadata schema.
2. **Template import:** streaming upload plus `pg_restore`/`psql` detection.
3. **Allocation:** stable worktree identity, advisory locks and template cloning.
4. **Lifecycle:** heartbeat, explicit release and TTL garbage collection.
5. **Agent integration:** CLI emits `DATABASE_URL` for the current worktree.
6. **Host reconciliation:** watcher heartbeats existing paths and releases deleted worktrees.
7. **Hardening next:** audit log, metrics and broader integration tests.

## Current cleanup semantics

The container cannot safely inspect arbitrary host worktree paths. Therefore it
uses leases as its fallback, while `dbbroker watch` performs path reconciliation
on the host. Run the watcher as a user service so deleted worktrees are cleaned
without relying on Git hooks.
