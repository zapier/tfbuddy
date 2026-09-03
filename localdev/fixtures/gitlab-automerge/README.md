# GitLab auto-merge Tilt fixture

This opt-in fixture provisions:

- two temporary Terraform Cloud workspaces;
- a third workspace configuration that is intentionally not created at startup;
- a temporary GitLab project with `main` and `test-change` branches;
- a `.tfbuddy.yaml` that maps one changed directory to each workspace;
- TFC and GitLab webhooks routed to the local TFBuddy through ngrok;
- local NATS and TFBuddy with `TFBUDDY_ALLOW_AUTO_MERGE=true`.

It uses the same `.env` credentials as the default Tilt environment.

```sh
tilt up -f localdev/fixtures/gitlab-automerge/Tiltfile
```

The initial Tilt load may need one manual refresh after Terraform creates the
external resources. Open the GitLab project from the `fixture-links` resource,
create an MR from `test-change` into `main`, and comment:

```text
tfc plan
tfc apply
```

The MR must remain open until both workspace applies complete successfully.
Targeted workspace applies may complete successfully but must not auto-merge.

## Delayed workspace regression test

The fixture also creates a `test-missing-workspace` branch that changes the
first existing workspace and the configured-but-missing delayed workspace.
This simulates a bootstrap MR where applying one workspace makes another
workspace available.

1. Create an MR from `test-missing-workspace` into `main`.
2. Run `tfc plan`. The delayed workspace should report that it does not exist.
3. Run `tfc apply -w <first-workspace-name>`. The run should succeed, but the
   MR must remain open because the delayed workspace is still required.
4. Trigger the manual `tf-create-delayed-workspace` resource in Tilt. It
   creates that workspace and its TFC webhook.
5. Run `tfc apply -w <delayed-workspace-name>`.

The second apply must auto-merge the MR because both affected workspaces have
now been applied at the same commit SHA. Workspace names and links are shown
by the fixture resources in Tilt.

Destroy all resources when finished:

```sh
tilt down -f localdev/fixtures/gitlab-automerge/Tiltfile
```
