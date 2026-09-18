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

> :no_entry: Apply failed. All jobs in pipeline 900 (running) must succeed before apply. Still waiting on: lint (failed), unit (running).

These jobs never block an apply:

* TFBuddy's own `TFC/*` statuses.
* Jobs marked `allow_failure: true`.
* Jobs in the `success`, `skipped`, or `manual` states.
* Jobs belonging to a different pipeline for the same commit.

If the commit has no pipeline other than the external one GitLab creates for
TFBuddy's own statuses, there is no CI to wait on and the apply proceeds.

This setting applies to GitLab only. The GitHub path is unaffected.

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
    # Merge MR once all workspaces have been applied. This is enabled by default, and can be disabled globally by setting TFBUDDY_ALLOW_AUTO_MERGE to false
    autoMerge: true
```

TF Buddy uses [doublestar](https://github.com/bmatcuk/doublestar#about) for its path matching. In the example above, the following directories/files would be watched:

* `terraform/$ENV` - anything that is a direct child of `terraform/production` or `terraform/staging`
* `terraform/production/**` - anything that has `terraform/production` as an ancestor
* `terraform/staging/**/*.tf` - any Terraform files that have `terraform/staging` as an ancestor
* `terraform/staging/{foo,bar}/**` - anything that has `terraform/staging/foo` or `terraform/staging/bar` as an ancestor
* `terraform/staging/**/[^0-9]*` - anything that has `terraform/staging` as an ancestor and does _not_ start with an integer
