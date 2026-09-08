---
name: dbbroker-worktree-db
description: "Trigger: DATABASE_URL, PostgreSQL for a worktree, isolated agent database, dbbroker. Acquire and manage an ephemeral database for the current Git worktree."
license: Apache-2.0
metadata:
  author: joaquin
  version: "1.2"
---

## Activation Contract

Use this skill when a task needs a PostgreSQL database isolated to the current Git worktree. Run the workflow from inside that worktree.

## Hard Rules

- Treat `main` as the default template unless the task names another.
- Acquire with `dbbroker acquire main --env-file <project-env-file>`; choose the environment file the repository already uses, defaulting to `.env.local` when no convention exists.
- Use only the assigned `dbb_wt_*` database. Treat `dbb_tpl_*` databases as immutable clone sources.
- Keep the database while its worktree exists. Run `dbbroker release` only when decommissioning that worktree or when explicitly requested.
- Resolve templates within the current repository. Run `dbbroker seed` only when explicitly asked to replace or create that repository's template.
- Report failures with the failing command and broker response; never silently fall back to a shared development database.

## Decision Gates

| Situation | Action |
|---|---|
| Broker unavailable | Check `curl --fail http://127.0.0.1:8787/healthz`; report if still unavailable |
| Existing assignment | Reuse the URL returned by `acquire` |
| Long-lived worktree | Rely on the host watcher; use `dbbroker heartbeat` if watcher status is unknown |
| Worktree removal | Release first, then remove the worktree |

## Execution Steps

1. Confirm `dbbroker` is on `PATH` and the health endpoint succeeds.
2. Acquire the database into the project environment file and verify it contains one non-empty `DATABASE_URL`.
3. Ensure the environment file is ignored by Git, then run the repository's normal migrations or setup against that URL.
4. Perform the requested development or test work.
5. Preserve or release the assignment according to the decision gates.

## Output Contract

State the assigned database name, setup performed, verification result, and whether the assignment remains active. Redact passwords and full connection URLs.

## References

- [`../../README.md`](../../README.md) — broker operation, API, lifecycle, and troubleshooting.
