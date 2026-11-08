# Architecture

TF Buddy is driven by different webhook events from either a Terraform Cloud workspace, or a supported VCS (Github/Gitlab). These events are either user commands in the form of comments, or changes in state (PR being opened/closed).

## Overview

![overview](img/overview.png)

Once you have TF Buddy deployed you need to register a webhook from each repo that you want TF Buddy to operate on. You also need to register a webhook from each Terraform Workspace that you want TF Buddy to interact with. Once that's configured TF buddy will kick into action when you open a new PR. TF Buddy will then execute a speculative plan against the branch and will report back the plan in a detailed manner.

![plan](img/plan.png)

If you're happy with the plan you can issue a apply command `tfc apply` or if you're operating on multiple workspaces you can target a specific workspace `tfc apply -w workspace_name`. TF Buddy will verify that the PR is approved if that's required on your repo. It will also reject any applies if the branch has conflicts. Once the apply starts TF Buddy will provide a link to the run in Terraform Cloud for you to follow along with.

![apply](img/apply.png)

Once the apply completes TF Buddy will update the PR indicating what was changed and if there was any errors.

#### Comment Cleanup

When `TFBUDDY_DELETE_OLD_COMMENTS` is set, TFBuddy automatically cleans up old discussion threads to keep MRs/PRs readable. Each comment is tagged with an invisible HTML marker identifying its workspace and action (plan or apply). When a new run completes, TFBuddy deletes older discussions that match the same workspace and action, keeping only the latest one. This is workspace-scoped: if your MR triggers runs in multiple workspaces, each workspace retains its own most recent plan and apply comment independently. Previous run URLs are collected into a collapsible "Previous TFC Urls" table on the latest comment.

Example of how an error is reported

![error](img/error.png)

A more detailed error message is provided if the error occurred within TF Buddy. If the error occurred during terraform plan or terraform apply it will not contain a detailed message. Instead the run link will take you to the Terraform workspace where you can debug the error.


### Workspace Fan-Out

When an MR/PR touches several Terraform workspaces, TFBuddy splits the work onto a dedicated NATS JetStream queue and processes each workspace independently. The MR/PR webhook subscriber clones the repo once, evaluates which workspaces are blocked by changes on the target branch, and publishes one fan-out message per remaining workspace. A separate workspace worker drains the queue, with each delivery getting its own `AckWait` window — a slow workspace can no longer hold the parent message past `AckWait` and trigger a redelivery (which previously caused duplicate runs).

Per-workspace state (discussion ID, root note ID) is local to the worker invocation, so concurrent deliveries cannot leak IDs across workspaces.

The fan-out is gated by `TFBUDDY_WORKSPACE_FANOUT_ENABLED` (default `true`). Setting it to `false` falls back to the legacy inline per-MR loop, useful if you need to roll back at runtime without redeploying.

Each fan-out message carries the upstream webhook delivery ID (`X-GitHub-Delivery` for GitHub, `X-Gitlab-Event-UUID`/`Idempotency-Key` for GitLab). That ID combined with the workspace name is used as the JetStream dedup key, so a single webhook fanning out to N workspaces produces N distinct keys, while a legitimate retrigger (a new push or comment within the dedup window) carries a fresh delivery ID and is *not* swallowed by the dedup window.

Target-branch evaluation runs once in the publisher, so the MR-level "could not evaluate target branch" warning is posted at most once per delivery. Each worker then re-clones the repo to upload the workspace directory to TFC, so an MR touching N workspaces still does N+1 clones in fan-out mode. On large monorepos this measurably increases git egress and per-message latency; mitigate via `TFBUDDY_GITHUB_CLONE_DEPTH` / `TFBUDDY_GITLAB_CLONE_DEPTH`, or disable fan-out (`TFBUDDY_WORKSPACE_FANOUT_ENABLED=false`) and plan every workspace in the hook worker instead.

The workspace stream exposes the same `consumers >= 1` liveness check as the other streams, wired into `/live` so a stuck or unsubscribed worker is caught by Kubernetes probes rather than silently piling up unprocessed messages.

### Long-running hook events

The hook workers and the workspace worker read their queues through JetStream pull consumers: `gitlab_mr_event_pull_worker`, `gitlab_note_event_pull_worker` and `github_comment_event_pull_worker` on the `HOOKS` stream, and `tfbuddy_workspace_trigger_pull_worker` on `TFBUDDY_WORKSPACE_TRIGGERS`. Each replica pulls one event, handles it, and only then pulls the next, so a burst of merge requests waits in the stream and the next free replica takes the next event. While an event is handled, TFBuddy extends its ack deadline every 20s, so an MR that takes minutes to plan is not handed to another replica to plan again. The consumers use a 60s `AckWait`, which only bounds how long a crashed replica holds an event, and at most 5 deliveries. A handler error retries after 30s; a permanent error, a panic or an undecodable message is dropped.

On startup TFBuddy deletes the push consumers older releases used (`gitlab_mr_event_worker`, `gitlab_note_event_worker`, `github_comment_event_worker`, `tfbuddy_workspace_trigger_worker`), because a work-queue stream allows one consumer per subject. Events they had not acked stay in the stream and go to the pull consumers. Once that has happened, an older release cannot create its push consumers again, so rolling back needs the pull consumers deleted first, for example `nats consumer rm HOOKS gitlab_mr_event_pull_worker`, and the same for the other pull consumers.

Each workspace dispatch is also recorded in the `WORKSPACE_DISPATCHES` KV bucket, keyed by the webhook delivery ID (or the GitLab note ID) and the workspace, for 2 hours. When a hook event is delivered again anyway, after a crash for example, the workspaces it already dispatched are skipped instead of planned a second time. A new push or `tfc plan` comment is a new trigger and always runs. The metrics `tfbuddy_jetstream_messages_settled` and `tfbuddy_jetstream_messages_redelivered` show how each queue's messages end and how often they are redelivered.

### Run events and polling

Run status events (`RUN_EVENTS`) and polling tasks (`RUN_POLLING`) go through the same kind of pull consumer: `gitlab_run_event_pull_worker` and `github_run_event_pull_worker` on `RUN_EVENTS`, and `run_polling_pull_worker` on `RUN_POLLING`. Each replica handles one message at a time, with the same ack deadline, retry and metrics behaviour as the hook queues. Older releases read these streams through push consumers that redelivered an event whenever it waited in a busy replica's buffer for more than 30s, so the same status was often handled several times, sometimes long after the run had finished.

A run event that the handler does not handle, for example because the TFC API call failed, is retried after 30s, at most 5 deliveries in all. A failed poll is not redelivered: the task goes back to the polling KV with its usual 10s delay, and the dispatcher sends it again from there.

On startup TFBuddy deletes the push consumers older releases used on these streams (`gitlab`, `github` and `polling`). Rolling back to such a release needs the pull consumers deleted first:

```
nats consumer rm RUN_EVENTS gitlab_run_event_pull_worker
nats consumer rm RUN_EVENTS github_run_event_pull_worker
nats consumer rm RUN_POLLING run_polling_pull_worker
```

### Metrics for SLOs

TFBuddy answers every webhook with `200 OK` and does the work later, so HTTP status codes don't show whether it is working. These metrics follow an event from the webhook to the final status on the merge request:

| Question | Metric |
|---|---|
| Did TFBuddy queue the webhook? | `tfbuddy_gitlab_webhook_success`, `tfbuddy_gitlab_webhook_failed{reason}` |
| How long did it wait before a replica picked it up? | `tfbuddy_jetstream_message_wait_seconds{queue}`, a histogram of each message's first delivery |
| Did the run's final status reach the merge request? | `tfbuddy_tfc_runs_finished{provider,runType,status}` against `tfbuddy_tfc_runs_started` |

Example SLIs, in the same order. A labelled counter has no series until it is first incremented, and a sum over no series returns no data, so the counters that can be missing are defaulted to zero:

```promql
# Share of webhooks queued
(sum(rate(tfbuddy_gitlab_webhook_success[1h])) or vector(0))
  / ((sum(rate(tfbuddy_gitlab_webhook_success[1h])) or vector(0))
     + (sum(rate(tfbuddy_gitlab_webhook_failed{reason=~"decode-error|publish-error"}[1h])) or vector(0)))

# p99 wait before a message is picked up, per queue
histogram_quantile(0.99, sum by (le, queue) (rate(tfbuddy_jetstream_message_wait_seconds_bucket[1h])))

# Share of started runs whose final status was reported
(sum(increase(tfbuddy_tfc_runs_finished[6h])) or vector(0)) / sum(increase(tfbuddy_tfc_runs_started[6h]))
```

With no webhooks or no started runs in the window, the first and last queries return no data or `NaN`, which means there was nothing to measure.

`tfbuddy_gitlab_webhook_failed` has one of four reasons: `invalid-token` and `invalid-event` for requests TFBuddy rejects, `decode-error` for a body it cannot read, and `publish-error` when the event could not be written to the `HOOKS` stream, for example while NATS is down. A run is counted in `tfbuddy_tfc_runs_finished` when the run event worker has handled its final status. JetStream delivers the final event again if its ack is lost, so the worker also records the run in the `WORKSPACE_DISPATCHES` bucket, and a later delivery of the same run is not counted. Runs take minutes, so compare the two counters over hours rather than minutes.

### TFC API Rate Limiter

The TFC API client wraps its HTTP transport with a token-bucket rate limiter (`TFBUDDY_TFC_RATE_LIMIT_RPS`, default `30`; burst `TFBUDDY_TFC_RATE_LIMIT_BURST`, default `30`) so concurrent workspace workers cooperatively stay under TFC's documented per-token limit. The client deliberately has no top-level `Timeout`: `ConfigurationVersions.Upload` streams the cloned repo and a fixed cap would truncate slow uploads on large repos. Per-call deadlines flow through `context.Context`.

### Webhook Management

At Zapier we automate the creation of all relevant webhooks by leveraging Terraform to create them. The example below is a resource we use to hook up a Gitlab project and a Terraform Cloud workspace to TF Buddy.

```terraform
resource "gitlab_project_hook" "tfbuddy" {

  project = "project"
  url     = var.tfbuddy_gitlab_webhook_url

  enable_ssl_verification = true

  merge_requests_events = true
  note_events           = true
  push_events           = true

  token = var.tfbuddy_gitlab_webhook_token
}

resource "tfe_notification_configuration" "tfbuddy" {

  name             = "tfbuddy"
  enabled          = true
  destination_type = "generic"
  triggers = [
    "run:created",
    "run:planning",
    "run:errored",
    "run:needs_attention",
    "run:applying",
    "run:completed"
  ]
  url          = var.tfbuddy_tfc_webhook_url
  workspace_id = "workspace_id"
}

```

