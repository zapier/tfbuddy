# TF Buddy

TFBuddy allows Terraform Cloud users to get apply-before-merge workflows in their Pull Requests.

## Terraform Cloud API Driven Runs

Terraform Cloud (TFC) has a native VCS integration that can trigger plans and applies based for repositories, however it
requires a merge after apply workflow that is not desirable in many cases. This tool has been developed to enable a 
apply-before-merge workflow. 

### How

This tool provides a server function that processes webhooks from Gitlab/Github, triggers a Run in TFC for the Merge Request 
and then passes status updates of those back to the Merge/Pull Request in the form of comments.

### Architecture

TFBuddy consists of the webhook handler and a NATS cluster.

![](./images/tfbuddy.png)

### Building

```
git clone ssh://git@github.com/zapier/tfbuddy.git
cd tfbuddy
go mod download
go test -v ./...
go build -v
```

