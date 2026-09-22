# GitLab auto-merge Tilt fixture

This opt-in fixture provisions:

- two temporary Terraform Cloud workspaces;
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

Destroy all resources when finished:

```sh
tilt down -f localdev/fixtures/gitlab-automerge/Tiltfile
```
