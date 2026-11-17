# Usage

## Installation

### Dependencies
1. Kubernetes Cluster
1. Gitlab / Github token
1. Terraform Cloud token
1. [NATS](https://nats.io/) (installed by TFBuddy helm chart)

### Helm

```console
helm repo add tfbuddy https://zapier.github.io/tfbuddy/
```

**For use with Github**

```console
export TFC_TOKEN="" \
       GITHUB_TOKEN=""

helm install tfbuddy charts/tfbuddy \
  --set secrets.env.TFC_TOKEN="${TFC_TOKEN}" \
  --set secrets.env.GITHUB_TOKEN="${GITHUB_TOKEN}" \
  --dependency-update
```

**For use with Gitlab**

```console
export TFC_TOKEN="" \
       GITLAB_TOKEN="" \

helm install tfbuddy charts/tfbuddy \
  --set secrets.env.TFC_TOKEN="${TFC_TOKEN}" \
  --set secrets.env.GITLAB_TOKEN="${GITLAB_TOKEN}" \
  --dependency-update
```

The default helm values can be found [here](https://github.com/zapier/tfbuddy/blob/main/charts/tfbuddy/values.yaml).

<!-- BEGIN GENERATED CONFIGURATION -->
## Configuration

`tfbuddy` can be configured to meet your specific setup through environment variables or flags.

The full list of supported environment variables and flags is described below:

|Env Var|Flag|Description|Default Value|
|---|---|---|---|
|`TFBUDDY_LOG_LEVEL`|`--log-level, -v`|Set the log output level (info, debug, trace)|`info`|
|`TFBUDDY_DEV_MODE`|`--dev-mode`|Enable developer-friendly console logging output.|`false`|
|`TFBUDDY_OTEL_ENABLED`|`--otel-enabled`|Enable OpenTelemetry export for TFBuddy.|`false`|
|`TFBUDDY_OTEL_COLLECTOR_HOST`|`--otel-collector-host`|OpenTelemetry collector host.||
|`TFBUDDY_OTEL_COLLECTOR_PORT`|`--otel-collector-port`|OpenTelemetry collector port.||
|`TFBUDDY_GITLAB_HOOK_SECRET_KEY`|`--gitlab-hook-secret-key`|Secret key used to validate incoming GitLab webhooks.||
|`TFBUDDY_GITHUB_HOOK_SECRET_KEY`|`--github-hook-secret-key`|Secret key used to validate incoming GitHub webhooks.||
|`TFBUDDY_DEFAULT_TFC_ORGANIZATION`|`--default-tfc-organization`|Default Terraform Cloud organization for workspaces that omit one in .tfbuddy.yaml.||
|`TFBUDDY_WORKSPACE_ALLOW_LIST`|`--workspace-allow-list`|Comma-separated workspace allow list. Entries without an organization use the default Terraform Cloud organization.||
|`TFBUDDY_WORKSPACE_DENY_LIST`|`--workspace-deny-list`|Comma-separated workspace deny list. Entries without an organization use the default Terraform Cloud organization.||
|`TFBUDDY_ALLOW_AUTO_MERGE`|`--allow-auto-merge`|Globally enable or disable TFBuddy-managed auto-merge.|`true`|
|`TFBUDDY_FAIL_CI_ON_SENTINEL_SOFT_FAIL`|`--fail-ci-on-sentinel-soft-fail`|Mark CI as failed when Terraform policy checks soft-fail.|`false`|
|`TFBUDDY_DELETE_OLD_COMMENTS`|`--delete-old-comments`|Delete older bot comments for the same workspace and action after posting a newer one.|`false`|
|`TFBUDDY_NATS_SERVICE_URL`|`--nats-service-url`|NATS connection URL. When empty, TFBuddy falls back to the NATS client default.||
|`TFBUDDY_GITLAB_PROJECT_ALLOW_LIST`|`--gitlab-project-allow-list`|Comma-separated GitLab project allow list prefixes.||
|`TFBUDDY_PROJECT_ALLOW_LIST`|`--project-allow-list`|Deprecated comma-separated GitLab project allow list prefixes.||
|`TFBUDDY_GITHUB_REPO_ALLOW_LIST`|`--github-repo-allow-list`|Comma-separated GitHub repository allow list prefixes.||
|`TFBUDDY_GITHUB_CLONE_DEPTH`|`--github-clone-depth`|Git clone depth to use for GitHub merge request checkouts. Zero means full history.|`0`|
|`TFBUDDY_GITLAB_CLONE_DEPTH`|`--gitlab-clone-depth`|Git clone depth to use for GitLab merge request checkouts. Zero means full history.|`0`|
|`TFBUDDY_TFC_RATE_LIMIT_RPS`|`--tfc-rate-limit-rps`|Client-side rate limit (requests per second) for the Terraform Cloud API. Tuned to match TFC's documented per-token limit and prevent 429s when many workspaces are triggered concurrently.|`30`|
|`TFBUDDY_TFC_RATE_LIMIT_BURST`|`--tfc-rate-limit-burst`|Burst capacity for the TFC API token-bucket rate limiter.|`30`|
|`TFBUDDY_JETSTREAM_DEDUP_WINDOW`|`--jetstream-dedup-window`|Window during which JetStream remembers a Nats-Msg-Id to dedupe republishes. Applied to the RUN_EVENTS stream. Accepts a Go duration string (e.g. 30m, 1h).|`30m0s`|
|`TFBUDDY_REQUIRE_PIPELINE_SUCCESS`|`--require-pipeline-success`|Refuse `tfc apply` while the merge request pipeline for the commit has a job that has not succeeded. Only takes effect for GitLab projects that also set `only_allow_merge_if_pipeline_succeeds`. TFBuddy's own TFC/* commit statuses are ignored, so a pending apply status does not block itself.|`false`|
|`TFBUDDY_BLOCK_APPLY_ON_DRAFT`|`--block-apply-on-draft`|Refuse `tfc apply` while the merge request is a Draft. A Draft cannot be merged, so an apply on one leaves live infrastructure ahead of the target branch. `tfc plan` is still allowed.|`false`|
|`TFBUDDY_STATUS_TRIGGER_WAIT`|`--status-trigger-wait`|How long `tfc status` waits for TFBuddy to publish a commit status for the pipeline's commit before failing the job. Accepts a Go duration string (e.g. 5m).|`5m0s`|
|`TFBUDDY_SKIP_AUTO_PLAN_LABELS`|`--skip-auto-plan-labels`|Comma-separated GitLab merge request labels (case-insensitive). When a merge request carries any of these labels, TFBuddy skips automatic `tfc plan` triggering from GitLab webhook events (open, reopen, and new commits pushed), but still discovers every affected workspace and posts a pending TFC/plan/<workspace> and TFC/apply/<workspace> commit status for each, without creating a Terraform Cloud run. Does not affect `tfc plan`/`tfc apply` run from an MR comment, and does not block cleanup when the MR is merged or closed.||
|`TFBUDDY_AUTO_MODE_ENABLED`|`--auto-mode-enabled`|Enable Auto Mode for GitLab: merge requests whose author is listed under `autoMode` in the target branch's .tfbuddy.yaml are planned with a change class in the run message and applied through the `tfc apply` path once they are approved after their last push. When disabled, those merge requests take Manual Mode, the normal `tfc apply` flow.|`false`|
<!-- END GENERATED CONFIGURATION -->

For sensitive environment variables use `secrets.envs` which can contain a list of key/value pairs
```yaml
secrets:
  create: true
  name: tfbuddy
  # envs can be used for writing sensitive environment variables
  # to the secret resource. These should be passed into the
  # deployment as arguments.
  # envs: []
```

An ingress resource is provided for setting external access.
```yaml
ingress:
  create: true
  annotations:
    kubernetes.io/ingress.class: nginx-external
    nginx.ingress.kubernetes.io/force-ssl-redirect: "true"
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
  hosts:
    - host: tfbuddy.example.com
      paths:
        - path: /hooks/
          pathType: Prefix
```

For `nats` helm specific configurations go [here](https://github.com/nats-io/k8s/tree/main/helm/charts/nats#jetstream)

##### Requiring a green pipeline before apply

Set `TFBUDDY_REQUIRE_PIPELINE_SUCCESS=true` to make TFBuddy refuse `tfc apply`
while CI for the merge request commit has not succeeded. This works the same way
as the existing approval and merge-conflict checks: TFBuddy posts the reason as a
merge request comment and does not start a Terraform Cloud run.

The gate only applies to GitLab projects that have **Settings > Merge requests >
Pipelines must succeed** turned on (`only_allow_merge_if_pipeline_succeeds` in
the API). GitLab already records whether a red pipeline should stop a merge, so
TFBuddy reads that setting instead of asking projects to opt in twice. Projects
that leave it off are never gated, whatever `TFBUDDY_REQUIRE_PIPELINE_SUCCESS`
is set to.

TFBuddy looks at the individual job statuses in the merge request pipeline for
the commit, not the pipeline's overall status. It has to, because TFBuddy
publishes its own `TFC/<action>/<workspace>` commit statuses into that same
pipeline, and `TFC/apply/<workspace>` is pending exactly when an apply is due. A
check on the pipeline's overall status would therefore block every apply.

Every outstanding job is named in the comment, not just the first, so all of
them must be green before the apply is accepted:

> :no_entry: Apply failed. All jobs in [pipeline 900](https://gitlab.com/acme/infra/-/pipelines/900) (running) must succeed before apply. Still waiting on: lint (failed), unit (running).

These jobs never block an apply:

* TFBuddy's own `TFC/*` statuses.
* Jobs marked `allow_failure: true`.
* Jobs in the `success`, `skipped`, or `manual` states.
* Jobs belonging to a different pipeline for the same commit.

If the commit has no pipeline other than the external one GitLab creates for
TFBuddy's own statuses, there is no CI to wait on and the apply proceeds.

This setting applies to GitLab only. The GitHub path is unaffected.

##### Refusing apply on a Draft

Set `TFBUDDY_BLOCK_APPLY_ON_DRAFT=true` to make TFBuddy refuse `tfc apply` on a
Draft merge request. GitLab will not merge a Draft, so an apply on one leaves
live infrastructure ahead of the target branch, and TFBuddy cannot merge it after
the apply succeeds. Like the approval and merge-conflict checks, TFBuddy posts
the reason as a merge request comment and does not start a Terraform Cloud run:

> :no_entry: Apply blocked: this MR is a Draft. Mark it ready, then run `tfc apply` again — TFBuddy merges it as soon as the apply succeeds.

`tfc plan`, `tfc lock`, and `tfc unlock` still work on a Draft, and there is no
per-comment override. TFBuddy reads the Draft state from the API when the
comment is processed, so marking the MR ready and commenting again is enough.

The setting also covers GitHub, where the same check refuses `tfc apply` on a
Draft pull request and asks for it to be marked ready for review.

##### Skipping automatic plan by label

Set `TFBUDDY_SKIP_AUTO_PLAN_LABELS` to a comma-separated list of GitLab merge
request labels (e.g. `TFBUDDY_SKIP_AUTO_PLAN_LABELS=do-not-plan,wip`) to make
TFBuddy skip the automatic `tfc plan` it would otherwise trigger when a merge
request is opened, reopened, or pushed to. The match is case-insensitive and
checks against any label on the merge request.

This only affects plans TFBuddy triggers on its own from a GitLab webhook.
Running `tfc plan` or `tfc apply` from an MR comment still works regardless of
labels, and cleanup when the MR is merged or closed (releasing locks, etc.)
still runs. This setting applies to GitLab only; it has no effect on GitHub
pull requests.

When a plan is skipped this way, TFBuddy still discovers every workspace the
merge request would have affected (the same discovery `tfc plan` uses) and
posts a `TFC/plan/<workspace>` and `TFC/apply/<workspace>` commit status, in
the `pending` state, for each one — without cloning the repository or
creating a Terraform Cloud run. This matters for merge requests that touch
many workspaces at once: without a per-workspace status, a newly-added
workspace could merge in having never been planned or applied at all. A
workspace already excluded by the allow/deny list still gets its usual
`skipped` status instead.

These pending statuses satisfy a step that waits for any `TFC/*` status
(such as the `tfc status` CI job behind `TFBUDDY_STATUS_TRIGGER_WAIT` above),
but — unlike a real plan/apply — they are never resolved to success or
failure, since no run actually exists to report on. If the GitLab project
also has **Pipelines must succeed** enabled, a pending status that never
resolves can block the merge request from merging; plan carefully before
combining the two.

##### .tfbuddy.yaml

To use TF Buddy in a given repo, place a file named `.tfbuddy.yaml` in its root, with contents similar to this:

```yaml
workspaces:
    # The actual name of the TFC workspace you want to control with TF Buddy
  - name: team_name_prod
    # The main directory (relative to this file) to monitor for changes
    dir: terraform/production/
    # Any additional directories (relative to this file) to monitor for changes
    triggerDirs:
      - terraform/production/**
    # Additional configuration, with a separate TFC workspace and directories
  - name: team_name_staging
    dir: terraform/staging/
    triggerDirs:
      - terraform/staging/**/*.tf
      - terraform/staging/{foo,bar}/**
      - terraform/staging/**/[^0-9]*
    # For GitLab, merge the MR once every affected workspace has completed a
    # successful, non-targeted apply for the current commit. This is enabled by
    # default, and can be disabled globally with TFBUDDY_ALLOW_AUTO_MERGE=false.
    autoMerge: true
```

TF Buddy uses [doublestar](https://github.com/bmatcuk/doublestar#about) for its path matching. In the example above, the following directories/files would be watched:

* `terraform/$ENV` - anything that is a direct child of `terraform/production` or `terraform/staging`
* `terraform/production/**` - anything that has `terraform/production` as an ancestor
* `terraform/staging/**/*.tf` - any Terraform files that have `terraform/staging` as an ancestor
* `terraform/staging/{foo,bar}/**` - anything that has `terraform/staging/foo` or `terraform/staging/bar` as an ancestor
* `terraform/staging/**/[^0-9]*` - anything that has `terraform/staging` as an ancestor and does _not_ start with an integer

##### Discovering workspaces from the directory layout

When workspace names follow the directory layout, a `discover` rule can stand in
for one entry per workspace. A repo with one workspace per
`services/<service>/<env>/<cell>/` directory needs only this:

```yaml
discover:
    # Every directory matching dir is a workspace. Each `*` matches exactly one
    # directory name; there is no `**` here.
  - dir: services/*/*/*/
    # Directories that dir matches but that are not workspaces.
    exclude:
      - services/*/*/files
    # {1}, {2}, {3} are the names the first, second and third `*` matched.
    # The name must use every `*`, so that two directories never share a name.
    name: services-{1}-{2}-{3}
    triggerDirs:
      - services/{1}/{2}
      - services/{1}/{2}/files/**
    # organization, mode and autoMerge work as they do for a workspace entry.
workspaces:
  - name: workspaces-root
    dir: workspaces/
```

With this rule, `services/api/staging/main-us-west-2/` is the workspace
`services-api-staging-main-us-west-2`, planned when an MR changes a file in that
directory, in `services/api/staging/`, or under `services/api/staging/files/`.

* Workspaces are found on the MR's source branch, so a new directory is planned
  in the MR that adds it, and a deleted one is no longer planned. When an MR is
  merged or closed, TF Buddy also looks on the target branch to release its
  locks, since the source branch may already be deleted.
* As with explicit entries, a change in a directory whose path ends with a
  workspace's `dir` also triggers it: `archive/services/api/staging/main-us-west-2/`
  triggers `services-api-staging-main-us-west-2`.
* TF Buddy only lists the directories an MR's changes can reach; for the rule
  above, that is one listing per `services/<service>/<env>/` changed. A
  `tfc plan -w <name>` for a workspace the MR does not touch lists everything
  the rule matches.
* An entry under `workspaces` takes precedence over a discovered workspace with
  the same name or `dir`, which is how to name one directory differently.
* If two rules give one directory different workspaces (a different name or
  organization), the run fails; exclude the directory from one rule. Rules
  that give it the same workspace combine their `triggerDirs`, and must agree
  on `mode` and `autoMerge`.
* If two directories produce the same workspace name, the run fails rather than
  pointing both at one workspace; add an explicit entry for one of them. With
  `services-{1}-{2}`, for example, `services/a-b/c/` and `services/a/b-c/` both
  produce `services-a-b-c`. TF Buddy checks for this even when the MR only
  touches one of them, by looking up every other directory that could produce
  each discovered name, so names with many separators cost a few more listings.
* YAML treats `{` as syntax at the start of a value and anywhere inside a
  `[a, b]` list, so quote such values (`name: "{1}-{2}"`) or use block lists
  as above.

### GitLab auto-merge

TFBuddy records the complete set of workspaces affected by each GitLab MR commit.
It requests GitLab auto-merge only after the latest non-targeted apply run for
every workspace succeeds. The merge request is pinned to that commit SHA, and
GitLab still enforces approvals, conflicts, and required pipeline checks.

Every affected workspace must use `mode: apply-before-merge` and have
`autoMerge: true`. If any affected workspace disables auto-merge, TFBuddy will
not auto-merge that commit. Targeted applies never satisfy the aggregate.

### Workspace locks

`tfc apply` locks the workspace for its merge request by tagging it
`tfbuddylock-<MR number>`. Until that MR merges or closes, TFBuddy refuses
applies to the workspace from any other MR. TFBuddy removes the tag when the MR
merges or closes; if that was missed, the next apply from another MR removes it
(by exact name) after checking that the MR has merged or closed. Every other
MR's tag on the workspace is checked this way, not just the first one.

#### Locks held by automation

Automation that applies through the Terraform Cloud API rather than an MR can
hold the same lock, so that TFBuddy and the automation never apply one
workspace at the same time. Its tag is `tfbuddylock-<holder>`, where `<holder>`
starts with a letter and uses only lowercase letters, digits, `-`, `_` and `:`.
Make the holder unique to each attempt, for example by including the workflow
run ID (`tfbuddylock-selfserve-payments-api-run-8f2c`), so that one attempt
removing its tag can never unlock the workspace under another attempt.

While such a tag is on a workspace, TFBuddy refuses `tfc apply` and names the
holder in the MR. It never removes the tag itself, because no MR state says when
it is stale; `tfc unlock -w <workspace>` clears every `tfbuddylock-*` tag on the
workspace, including this one, if a holder fails without releasing it.

Tags are not an atomic lock, so a holder acquires it in this order:

1. List the workspace's tags matching `tfbuddylock`. If any lock tag other
   than its own is present, wait and retry.
2. Add its own `tfbuddylock-<holder>` tag.
3. List the tags again. If any lock tag other than its own is present, remove
   its own tag and retry later.
4. Run the plan and apply.
5. Remove its own tag by its exact name.

If either listing fails, treat the workspace as locked: do not apply, and if
the tag was added in step 2, remove it.

TFBuddy follows the same order: after adding its tag it lists the tags again
and backs off if any lock other than its own is present. It also refuses the
apply when it cannot list the tags. Backing off keeps the MR's tag, because
every apply attempt for an MR shares that tag and another attempt may already
be running under it. The MR therefore holds the workspace until it merges or
closes, as after any apply. If two MRs collide this way, both stay locked until
one of them runs `tfc unlock -w <workspace>`.

### Auto Mode: apply on approval

Auto Mode is for automation, on GitLab only. An Auto Mode merge request is applied
automatically once it is approved, through the same path as a `tfc apply`
comment: the same lock, statuses and divergence check, then auto-merge. Merge
requests from people, and every merge request while Auto Mode is off, take
Manual Mode: the normal flow, where someone comments `tfc apply`.

Turn it on with `TFBUDDY_AUTO_MODE_ENABLED=true`, and list the automation users in
`.tfbuddy.yaml`:

```yaml
autoMode:
  authors:
    # Matched against the merge request author from the GitLab API.
  - username: tags-bot
    # The git author and committer emails its commits carry.
    commitEmails: [tags-bot@example.com]
  - username: renovate-bot
    # Renovate's own commits, plus the commits of a fix job that pushes to
    # Renovate's MRs to fix module inputs after a bump.
    commitEmails: [bot@renovateapp.com, renovate-fix-bot@example.com]
  # Users whose approval is automatic, such as an approval bot. See
  # "Falling back to Manual Mode".
  automaticApprovers: [approver-bot]
  # CI jobs that must have succeeded before an approval applies the MR, such
  # as an AI review job.
  requiredJobs: ["<AI review job>"]
  # More directories, at any depth, that no Auto Mode MR may change, on top of
  # `.gitlab/` and `.gitlab-ci.yml`: for example, the directory that holds an
  # AI review's prompts.
  protectedDirs: [.ai-review]
  classes:
  - name: tags
    authors: [tags-bot]
    paths: ["services/*/*/*/tags.auto.tfvars.json"]
    # TFC workspace names. A class without workspaces matches no MR.
    workspaces: ["app-*"]
  - name: renovate-safe-plan
    authors: [renovate-bot]
    # Only Terraform files: the Sentinel class policy cannot see anything else.
    paths: ["services/**/*.tf", "services/**/.terraform.lock.hcl", "workspaces/**/*.tf", "workspaces/**/.terraform.lock.hcl"]
    workspaces: ["app-*", "workspaces-app-*"]
```

* TFBuddy reads `autoMode` only from the project's **default** branch, and
  only for a merge request that targets the default branch, so a merge request
  can neither add its own author nor widen a class. A merge request into any
  other branch is a Manual Mode MR, whatever that branch's `.tfbuddy.yaml`
  says. An MR that fell back to Manual Mode (see below) and is then
  retargeted to another branch stays fallen back, so TFBuddy reads the
  default branch's `autoMode` for it to find the automatic approvers. The
  workspaces it plans still come from the source branch, as for any MR.
* A merge request runs in Auto Mode when its author, read from the GitLab API, is
  under `authors`. The note author and the commit author are never used.
* Its change class is the first class whose `authors` include the MR author,
  whose `paths` (doublestar globs) match every file the MR changes, and whose
  `workspaces` (doublestar globs over TFC workspace names) match every
  workspace the MR affects, plus the one named by `tfc plan -w` or
  `tfc apply -w`. A class without `workspaces` matches no merge request, and
  neither does a merge request that affects no workspace. A merge
  request that fits no class falls back to Manual Mode, and so does one with any
  commit whose git author or committer email is not one of the author's
  `commitEmails`. Those emails are
  declared by whoever makes the commit, so this check is evidence, not proof:
  anyone who can push to the branch can claim them.
* Whatever the classes say, a merge request that changes any file under a
  `.gitlab/` directory or a directory listed in `protectedDirs`, or any file
  named `.gitlab-ci.yml` or `.tfbuddy.yaml`, at any depth, is a Manual Mode
  MR. The CI files decide what CI runs, including the review jobs that Auto
  Mode relies on, so list the directories that configure those reviews in
  `protectedDirs`. `.tfbuddy.yaml`, which TFBuddy reads from the MR's own
  branch, maps the MR's files to workspaces and their TFC organizations.
  Names match case-insensitively.
* Every plan and apply run of an Auto Mode merge request has a run message that
  TFBuddy builds from fixed fields only, without the MR title:
  `MR [!123] [tfbuddy-class=tags] sha=<head SHA> author=tags-bot`.
  A Sentinel policy reads the class there. TFBuddy refuses to create an Auto Mode
  run without a class. Runs of other merge requests keep the title, so a
  marker in a person's title can only restrict their own run.
* An Auto Mode run is made from exactly the commit its run message names:
  TFBuddy checks out that SHA in its clone, rather than run whatever the
  source branch's tip is by then, and checks divergence from it. When the
  clone does not contain the commit (a force-push dropped it, or
  `TFBUDDY_GITLAB_CLONE_DEPTH` is too shallow for a branch that moved on),
  TFBuddy creates no run and fails the workspace statuses. That commit's class
  fails, but the merge request does not fall back to Manual Mode for it: its
  new head is planned by its own push. The class is decided from the MR's
  current changes, which are its head's, so TFBuddy also refuses an Auto Mode
  plan or apply of a commit that is no longer the head, such as one a delayed
  push event or comment asks for.
* Every run of an Auto Mode author's MR that fell back to Manual Mode is made
  from exactly its commit too: the author is automation that may push again
  at any time, such as a rebase, so the commit a person approved is the one
  that runs. Its `tfc apply` of a commit that is no longer the head is
  refused. This holds even after its author is removed from `authors`.
* TFBuddy posts a `TFC/class` commit status on the head commit: pending while
  the plans run, and success once every affected workspace's plan has passed.
  When any plan errored, was canceled, failed a policy (soft failures
  included), or a workspace the MR affects did not plan, the merge request
  falls back to Manual Mode, as below, and `TFC/class` becomes `skipped`.
  TFBuddy records
  each plan's outcome in NATS (the `AUTO_MODE_CLASS_PLANS` key-value bucket)
  with compare-and-swap, so when the last plans finish together exactly one of
  them sees every outcome and sets the result. When an outcome or `TFC/class`
  cannot be written, or the MR's mode cannot be read, the event is handled
  again later, and a finished class whose `TFC/class` was not written is
  reported by that retry. A redelivered MR event keeps the plans its first
  delivery began; a new trigger for the same commit, such as a `tfc plan`
  comment, begins them again, and the runs and outcomes of the plans it
  replaced no longer count; a late redelivery of the replaced trigger leaves the newer plans
  alone. TFBuddy does not yet read a Sentinel class-policy result.
* Once every plan has finished, TFBuddy posts one comment on the MR, and
  updates that same comment for later commits, with the class, the head SHA,
  each workspace's plan run ID and result, and, for an MR in Manual Mode, why.

#### Falling back to Manual Mode

Once an Auto Mode author's merge request falls back to Manual Mode, it stays in
Manual Mode until it merges. It falls back when:

* it fits no class: a commit fails the author check, a changed file is outside
  the class paths or is a denied path, or a workspace is outside the class;
* a class check fails: a plan with the class marker does not pass, or a
  workspace does not plan;
* an Auto Mode apply soft-fails a policy (see below), errors, or cannot be
  started for a workspace, for example because another MR holds its lock.
  TFBuddy does not tell a failed class policy from any other error, so any
  errored Auto Mode apply falls back;
* a person comments `tfc apply` while the Auto Mode gate does not pass (see
  below): the person takes the MR over.

Every fallback takes the same path:

1. TFBuddy records the fallback for the merge request (project and IID) in
   NATS, in the `AUTO_MODE_MERGE_REQUESTS` key-value bucket, which has no TTL.
   It does not use a label, which anyone with Developer access can edit.
2. `TFC/class` becomes `skipped`, with the description `Manual Mode: <reason>`,
   on the commit that fell back, and every later plan of the MR does the
   same. `skipped` never passes the Auto Mode gate, and an approval bot that
   waits for `success` never approves the MR; unlike `failed`, it
   does not turn the pipeline red, so the Manual Mode MR can merge once it is
   applied. `TFC/class` is `failed` only when TFBuddy could not record the
   fallback, after retrying for a few seconds, or could not read it back. A
   fallback seen in a run event is then retried with the event.
3. The first fallback posts a comment that says why.
4. TFBuddy plans the head commit again without the class, so that a person
   reviews Manual Mode plans. The exceptions are a fallback decided while
   TFBuddy plans the commit (the MR fits no class), whose plan is already a
   Manual Mode plan, and a takeover by `tfc apply`, whose apply plans without
   the class. The plan never runs in the callback that saw the failure:
   TFBuddy publishes a merge request event of its own
   (`tfbuddy_manual_mode_replan`) to the hooks stream, and the merge request
   event worker plans the commit like a push, unless the MR is no longer open.
   When the MR has moved on to a newer head, TFBuddy plans that head instead:
   its push may have decided its mode before the fallback, even if it is
   still planning. A head its push already planned in Manual Mode is then
   planned twice. If the request cannot be published, the comment asks for a
   `tfc plan`.

From then on no plan or apply of the merge request carries the class marker,
whatever later pushes, force-pushes or comments change, so the hard-mandatory
class policy does not fail it again, and its approval starts no apply. A person
approves it and someone comments `tfc apply`, as for any Manual Mode MR.
TFBuddy deletes the record once the merge request merges. A closed merge
request keeps it, so reopening it does not bring Auto Mode back. If the record
cannot be read, TFBuddy runs nothing for the merge request rather than guess
its mode.

Automation may have approved the MR before it fell back: an approval bot,
for example, may approve on a green `TFC/class` and, as a code owner of the
class files, its approval alone can satisfy GitLab's approval rules. So for a
fallen-back MR, TFBuddy refuses `tfc apply`, and says why, unless at least one
current approval comes from a user who is neither listed under
`automaticApprovers` nor an Auto Mode author. This holds even if the author is
later removed from `authors`; if the default branch no longer has an
`autoMode` section to tell automatic approvers apart, TFBuddy refuses the
apply:

```yaml
autoMode:
  automaticApprovers: [approver-bot]
```

Known limits: a transient TFC error on a class plan or an Auto Mode apply, or
a lock another MR or automation holds when an Auto Mode apply starts, puts
the MR in Manual Mode for good; a commit that is not in TFBuddy's clone fails
its runs without a fallback; and an MR whose changes trigger no workspace is
not judged, so it gets no fallback either.

When GitLab sends the `approved` merge request event (the one sent once every
approval rule is satisfied), TFBuddy applies an Auto Mode merge request only when
all of these hold, and otherwise says why on the MR:

1. The MR is open, not a Draft and has no conflicts.
2. It is approved. Turn on GitLab's "reset approvals on push" in the repo, so
   that every push removes the approvals and an approval always covers the
   head commit.
3. Every `TFC/plan/<workspace>` status on the head commit is green, and
   `TFC/class` has passed. Only statuses that TFBuddy's own GitLab account
   posted count. Anyone with Developer access can post a status with any
   name, so a `TFC/plan/*` or `TFC/class` status from anyone else is ignored,
   and one of those names that only someone else posted blocks the apply.
   The head commit's class plans recorded in NATS must also have finished
   and passed, with the class the apply would use: `TFC/class` is only what
   TFBuddy last wrote to GitLab, and a write that failed, or a late one from
   plans begun again since, can leave it green.
4. The head commit has a CI pipeline of this MR (a merge request pipeline)
   or, without one, of its source branch (a push pipeline), and every job in
   the newest one has succeeded, whatever `TFBUDDY_REQUIRE_PIPELINE_SUCCESS`
   says. No other pipeline of the commit counts: a pipeline of another MR or
   branch with the same commit reviews other changes, and one started by
   hand, through the API, a trigger or a schedule can carry variables a person
   set. As for `tfc apply`, a `manual` or `skipped` job, and a job allowed to
   fail, does not block.
5. Every job listed under `autoMode.requiredJobs` is in that pipeline, and its
   latest run has succeeded. Here a `manual`, `skipped` or allowed-to-fail job
   does not count: a review job that never ran does not pass. TFBuddy reads
   the jobs from GitLab's pipeline jobs API, which lists only jobs the pipeline
   ran from its CI config, so a commit status posted with a job's name cannot
   stand in for one. A job in a child pipeline is not seen, and so blocks. With
   no `requiredJobs`, nothing beyond step 4 is required, so list the review
   jobs an automatic approver relies on, such as an AI review job:

   ```yaml
   autoMode:
     requiredJobs: ["<AI review job>"]
   ```
6. Auto-merge is on and every affected workspace has `autoMerge: true`, because
   Auto Mode must merge what it applies.

An `approved` event for a commit that is no longer the MR's head starts
nothing and posts nothing: the head's own approval sends its own event. The
apply reads the head again, and runs nothing if it has moved on.

A `tfc apply` comment on an Auto Mode merge request takes the same checks
after the usual ones for `tfc apply`. When they all hold, the apply runs in
Auto Mode, as on approval. When one does not, for example because a review
job failed or `TFC/class` did not pass, the person who commented takes the MR
over: it falls back to Manual Mode until it merges, and the apply runs at
once as a Manual Mode apply, without the class marker. Like any fallen-back
MR's, it needs an approval from a person, so a person can always fix and
apply what automation could not. If TFBuddy cannot record the takeover,
nothing runs.

The apply then decides the MR's mode again, through the same code as a plan,
and runs nothing if the MR no longer runs in Auto Mode: an approval never
starts a Manual Mode apply. The lock check then refuses workspaces another MR
or automation holds, and the divergence check refuses workspaces whose paths
changed on the target branch.
For Auto Mode, a divergence check that cannot run blocks every workspace instead
of posting a warning; for a plan, that is a failed class check. An Auto Mode
apply that errors, or that TFBuddy cannot start for a workspace, falls back to
Manual Mode, so a retry is a Manual Mode `tfc apply`, which needs a person's
approval.

TFBuddy never overrides a Sentinel policy for Auto Mode. When an Auto Mode
apply soft-fails a policy (TFC reports a run that would apply as
`policy_override`, and a plan-only run as `policy_soft_failed`; both are
handled), TFBuddy puts the merge request in Manual Mode through the same
fallback as above, re-plan included, then discards the run in TFC, fails
`TFC/apply/<workspace>`, and comments that the apply was discarded and why.
The fallback comes first because a discarded run sends no event that could
retry it: if TFBuddy cannot record the fallback, it leaves the run waiting
for an override, which applies nothing, and handles the event again later.
If the discard fails, the comment says
so: discard the run in TFC by hand, and do not override the policy. Other
workspaces of the same apply are not affected, and the MR does not auto-merge,
because that workspace never applied. A Manual Mode apply that soft-fails
works as before: a person decides in TFC.
