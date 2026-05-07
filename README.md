# TFBuddy

TFBuddy allows Terraform Cloud users to get apply-before-merge workflows in their Pull Requests.

## Terraform Cloud API Driven Runs

Terraform Cloud (TFC) has a native VCS integration that can trigger plans and applies based for repositories, however it
requires a merge after apply workflow that may not be desirable in some cases. This tool has been developed to enable a 
apply-before-merge workflow. 

### How it works

This tool provides a server function that processes webhooks from Gitlab/Github, triggers a Run in TFC for Merge/Pull Requests
and then passes status updates of those Runs back to the Merge/Pull Request in the form of comments.

For MRs with multiple workspaces, TFBuddy tracks each workspace independently and can automatically clean up old plan/apply comments, keeping only the most recent one per workspace and action type. Set `TFBUDDY_DELETE_OLD_COMMENTS` to enable this.

Each touched workspace is dispatched onto a dedicated NATS JetStream queue and processed independently, so a single MR with many workspaces can no longer hold the webhook subscriber past `AckWait` (which previously caused JetStream redeliveries and duplicate TFC runs). The TFC API client is rate-limited (`TFBUDDY_TFC_RATE_LIMIT_RPS` / `_BURST`) so concurrent workers stay under TFC's documented per-token limit.


### Architecture

TFBuddy consists of the webhook handler and a NATS cluster.

![](./docs/img/overview.png)


## Installation

### Helm

See [Installation Docs](https://tfbuddy.readthedocs.io/en/stable/usage/)

## Contributing

The [contributing](https://tfbuddy.readthedocs.io/en/stable/contributing/) has everything you need to start working on TFBuddy.


## Documentation

To learn more about TF Buddy [go to the complete documentation](https://tfbuddy.readthedocs.io/).

---

Made by SRE Team @ ![zapier](https://zapier-media.s3.amazonaws.com/zapier/images/logo60orange.png)