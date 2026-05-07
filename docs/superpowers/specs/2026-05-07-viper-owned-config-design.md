# Viper-Owned Config Design

**Date:** 2026-05-07

## Goal

Replace direct reads of TFBuddy-owned environment variables with Viper-backed configuration so repo-owned settings follow one consistent access path.

## Scope

In scope:

- `TFBUDDY_*` settings owned by this repo
- Parsed booleans, strings, and comma-separated lists currently read with `os.Getenv`
- Default handling for repo-owned settings

Out of scope:

- Provider credentials such as `TFC_TOKEN`, `TFE_TOKEN`, `GITHUB_TOKEN`, `GITLAB_TOKEN`, and `GITLAB_ACCESS_TOKEN`
- Runtime CI variables such as `CI_PROJECT_ID`, `CI_COMMIT_SHA`, and `CI_MERGE_REQUEST_IID`
- Terraform Cloud workspace selection variables such as `TFC_WORKSPACE_NAME`

## Current Problems

- Viper is initialized centrally in `cmd/root.go`, but many packages bypass it and read `os.Getenv` directly.
- Boolean parsing is duplicated and inconsistent.
- Comma-separated list parsing lives at call sites instead of in one config layer.
- Raw config key strings are spread across the codebase.

## Proposed Approach

Introduce a small `internal/config` package backed by Viper that exposes typed helpers for TFBuddy-owned configuration.

Responsibilities:

- Define canonical config key names for repo-owned settings
- Bind defaults in one place
- Provide typed accessors for booleans, strings, and lists
- Keep parsing behavior consistent and close to Viper

Call sites that currently use `os.Getenv("TFBUDDY_*")` will switch to this package. Non-owned environment variables remain untouched.

## Initial Config Surface

- Logging:
  - `TFBUDDY_LOG_LEVEL`
  - `TFBUDDY_DEV_MODE`
- Telemetry:
  - `TFBUDDY_OTEL_ENABLED`
  - `TFBUDDY_OTEL_COLLECTOR_HOST`
  - `TFBUDDY_OTEL_COLLECTOR_PORT`
- Hooks:
  - `TFBUDDY_GITLAB_HOOK_SECRET_KEY`
  - `TFBUDDY_GITHUB_HOOK_SECRET_KEY`
- Trigger behavior:
  - `TFBUDDY_DEFAULT_TFC_ORGANIZATION`
  - `TFBUDDY_WORKSPACE_ALLOW_LIST`
  - `TFBUDDY_WORKSPACE_DENY_LIST`
  - `TFBUDDY_ALLOW_AUTO_MERGE`
- Comment/status behavior:
  - `TFBUDDY_FAIL_CI_ON_SENTINEL_SOFT_FAIL`
  - `TFBUDDY_DELETE_OLD_COMMENTS`
- Runtime wiring:
  - `TFBUDDY_NATS_SERVICE_URL`

## Implementation Notes

- Keep Viper initialization in `cmd/root.go`.
- Add one config initialization function that sets defaults after `viper.AutomaticEnv()`.
- Prefer Viper accessors over re-parsing string env values at call sites.
- For list values, use a helper that trims whitespace and drops empty items.
- Preserve existing behavior where default semantics matter, especially:
  - auto-merge enabled unless explicitly set to `false`
  - NATS URL falls back to `nats.DefaultURL`
  - sentinel soft-fail remains false unless explicitly enabled

## Testing

- Add unit coverage for list parsing and bool/default behavior in the new config package.
- Update or add targeted tests for packages whose behavior depends on migrated settings.
- Run the full Go test suite after the migration.

## Risks

- Viper key naming can drift if accessors and env bindings do not agree.
- Changing bool parsing semantics could subtly alter defaults if not preserved.
- Some code paths may rely on empty string versus unset behavior; tests should lock that down.
