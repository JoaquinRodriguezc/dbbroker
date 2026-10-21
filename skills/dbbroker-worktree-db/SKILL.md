---
name: dbbroker-worktree-db
description: "Gives the current Git worktree its own disposable PostgreSQL database and Redis namespace via dbbroker, so agents never share the development database or Redis. Trigger: DATABASE_URL, REDIS_URL, PostgreSQL or Redis for a worktree, isolated agent database, dbbroker."
license: Apache-2.0
metadata:
  author: joaquin
  version: "1.3"
---

## Activation Contract

Use this skill when a task needs a PostgreSQL database or Redis isolated to the current Git worktree. Run the workflow from inside that worktree.

## Hard Rules

- Treat `main` as the default template unless the task names another.
- Acquire with `dbbroker acquire main --env-file <project-env-file>`; choose the environment file the repository already uses, defaulting to `.env.local` when no convention exists.
- Use only the assigned `dbb_wt_*` database. Treat `dbb_tpl_*` databases as immutable clone sources.
- Keep the database while its worktree exists. Run `dbbroker release` only when decommissioning that worktree or when explicitly requested.
- Resolve templates within the current repository. Run `dbbroker seed` only when explicitly asked to replace or create that repository's template.
- When `acquire` writes `REDIS_URL` and `REDIS_KEY_PREFIX`, connect with exactly that URL (it carries a per-worktree username) and put every key and pub/sub channel under the prefix; for BullMQ use the prefix without its trailing colon as `prefix`. `NOPERM` means a key or command escaped the prefix: fix the app configuration, never switch to the admin credentials.
- Report failures with the failing command and broker response; never silently fall back to a shared development database or Redis.

## Decision Gates

| Situation | Action |
|---|---|
| Broker unavailable | Check `curl --fail http://127.0.0.1:8787/healthz`; report if still unavailable |
| Existing assignment | Reuse the URLs returned by `acquire` |
| App reads `REDIS_HOST`/`REDIS_PORT`/`REDIS_PASSWORD` only | Derive them from `REDIS_URL` and also pass its username; report if the app has no username setting |
| Redis `WRONGPASS` after a restart | Run `dbbroker heartbeat`, then retry |
| Long-lived worktree | Rely on the host watcher; use `dbbroker heartbeat` if watcher status is unknown |
| Worktree removal | Release first, then remove the worktree |

## Execution Steps

1. Confirm `dbbroker` is on `PATH` and the health endpoint succeeds.
2. Acquire into the project environment file and verify it contains one non-empty `DATABASE_URL` and, when the broker has Redis enabled, one `REDIS_URL` and `REDIS_KEY_PREFIX`.
3. Ensure the environment file is ignored by Git, then run the repository's normal migrations or setup against that URL.
4. Perform the requested development or test work.
5. Preserve or release the assignment according to the decision gates.

## Output Contract

State the assigned database name, Redis key prefix (if any), setup performed, verification result, and whether the assignment remains active. Redact passwords and full connection URLs.

## References

- [`../../README.md`](../../README.md) — broker operation, API, lifecycle, and troubleshooting.
