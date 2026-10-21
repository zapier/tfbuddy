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
|`TFBUDDY_WORKSPACE_FANOUT_ENABLED`|`--workspace-fanout-enabled`|Enable per-workspace JetStream fan-out (one NATS message per workspace) to keep AckWait windows scoped per workspace. When disabled, TFBuddy falls back to the inline per-MR loop.|`true`|
|`TFBUDDY_WORKSPACE_JETSTREAM_REPLICAS`|`--workspace-jetstream-replicas`|JetStream replica count for the workspace-trigger stream. Use 1 for single-node NATS or local dev; set to your NATS cluster size (often 3) in production for durability.|`1`|
|`TFBUDDY_TFC_RATE_LIMIT_RPS`|`--tfc-rate-limit-rps`|Client-side rate limit (requests per second) for the Terraform Cloud API. Tuned to match TFC's documented per-token limit and prevent 429s when many workspaces are triggered concurrently.|`30`|
|`TFBUDDY_TFC_RATE_LIMIT_BURST`|`--tfc-rate-limit-burst`|Burst capacity for the TFC API token-bucket rate limiter.|`30`|
|`TFBUDDY_JETSTREAM_DEDUP_WINDOW`|`--jetstream-dedup-window`|Window during which JetStream remembers a Nats-Msg-Id to dedupe republishes. Applied to RUN_EVENTS and TFBUDDY_WORKSPACE_TRIGGERS streams. Accepts a Go duration string (e.g. 30m, 1h).|`30m0s`|
|`TFBUDDY_REQUIRE_PIPELINE_SUCCESS`|`--require-pipeline-success`|Refuse `tfc apply` while the merge request pipeline for the commit has a job that has not succeeded. Only takes effect for GitLab projects that also set `only_allow_merge_if_pipeline_succeeds`. TFBuddy's own TFC/* commit statuses are ignored, so a pending apply status does not block itself.|`false`|
|`TFBUDDY_BLOCK_APPLY_ON_DRAFT`|`--block-apply-on-draft`|Refuse `tfc apply` while the merge request is a Draft. A Draft cannot be merged, so an apply on one leaves live infrastructure ahead of the target branch. `tfc plan` is still allowed.|`false`|
|`TFBUDDY_STATUS_TRIGGER_WAIT`|`--status-trigger-wait`|How long `tfc status` waits for TFBuddy to publish a commit status for the pipeline's commit before failing the job. Accepts a Go duration string (e.g. 5m).|`5m0s`|
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
