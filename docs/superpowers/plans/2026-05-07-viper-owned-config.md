# Viper-Owned Config Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route every TFBuddy-owned configuration read through Viper instead of direct environment access.

**Architecture:** Add a small `internal/config` package that defines canonical keys, defaults, and typed getters on top of Viper. Migrate repo-owned call sites to that package while leaving external credentials and CI variables untouched.

**Tech Stack:** Go, Cobra, Viper, Go test

---

### Task 1: Add the config access layer

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`
- Modify: `cmd/root.go`

- [ ] **Step 1: Write the failing tests**

Add tests for:
- parsing comma-separated lists with trimming and empty item removal
- bool defaults for auto-merge, delete-old-comments, dev mode, and sentinel soft-fail
- string fallback behavior for values like NATS URL

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config`
Expected: FAIL because the package and helpers do not exist yet.

- [ ] **Step 3: Write the minimal implementation**

Add the config package with:
- canonical keys/constants for TFBuddy-owned settings
- `Init()` to register defaults
- typed getters for strings, bools, and string lists

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/config`
Expected: PASS

- [ ] **Step 5: Commit**

Run:
```bash
git add cmd/root.go internal/config/config.go internal/config/config_test.go
git commit -m "refactor: add viper-backed config accessors"
```

### Task 2: Migrate owned config call sites

**Files:**
- Modify: `cmd/root.go`
- Modify: `internal/logging/logging.go`
- Modify: `pkg/allow_list/common.go`
- Modify: `pkg/comment_formatter/tfc_status_update.go`
- Modify: `pkg/gitlab_hooks/handler.go`
- Modify: `pkg/nats/nats_client.go`
- Modify: `pkg/tfc_trigger/project_config.go`
- Modify: `pkg/tfc_trigger/workspace_allowdeny_list.go`
- Modify: `pkg/vcs/common.go`
- Modify: `pkg/vcs/github/client.go`
- Modify: `pkg/vcs/github/hooks/github_hooks_handler.go`
- Modify: `pkg/vcs/gitlab/client.go`
- Modify: `pkg/vcs/gitlab/mr_status_updater.go`

- [ ] **Step 1: Write targeted failing tests or extend existing coverage**

Focus on:
- workspace allow/deny parsing
- global auto-merge default semantics
- delete-old-comments bool behavior

- [ ] **Step 2: Run the targeted tests to verify failure**

Run:
```bash
go test ./pkg/tfc_trigger ./pkg/vcs ./pkg/vcs/github ./pkg/vcs/gitlab
```
Expected: FAIL or require test updates because migrated accessors are not wired yet.

- [ ] **Step 3: Write the minimal implementation**

Replace direct `os.Getenv("TFBUDDY_*")` reads with the new config helpers, preserving current defaults and messages.

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run:
```bash
go test ./pkg/tfc_trigger ./pkg/vcs ./pkg/vcs/github ./pkg/vcs/gitlab
```
Expected: PASS

- [ ] **Step 5: Commit**

Run:
```bash
git add cmd/root.go internal/logging/logging.go pkg/allow_list/common.go pkg/comment_formatter/tfc_status_update.go pkg/gitlab_hooks/handler.go pkg/nats/nats_client.go pkg/tfc_trigger/project_config.go pkg/tfc_trigger/workspace_allowdeny_list.go pkg/vcs/common.go pkg/vcs/github/client.go pkg/vcs/github/hooks/github_hooks_handler.go pkg/vcs/gitlab/client.go pkg/vcs/gitlab/mr_status_updater.go
git commit -m "refactor: route tfbuddy config through viper"
```

### Task 3: Verify the migration end to end

**Files:**
- Modify: `README.md` if config docs need cleanup

- [ ] **Step 1: Run full verification**

Run:
```bash
go test ./...
```
Expected: PASS

- [ ] **Step 2: Update docs only if needed**

If any user-facing config wording now needs clarification, update `README.md` or leave docs unchanged.

- [ ] **Step 3: Commit**

Run:
```bash
git add README.md
git commit -m "docs: clarify viper-backed tfbuddy config" || true
```
