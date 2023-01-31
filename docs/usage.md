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
       GITLAB_TOKEN_USER=""

helm install tfbuddy charts/tfbuddy \
  --set secrets.env.TFC_TOKEN="${TFC_TOKEN}" \
  --set secrets.env.GITLAB_TOKEN="${GITLAB_TOKEN}" \
  --set secrets.env.GITLAB_TOKEN_USER="${GITLAB_TOKEN_USER}" \
  --dependency-update
```

The default helm values can be found [here](https://github.com/zapier/tfbuddy/blob/main/charts/tfbuddy/values.yaml).

### Configuration

Set the necessary environment variables for your setup.
```yaml
env:
  TFBUDDY_LOG_LEVEL: info
  TFBUDDY_NATS_SERVICE_URL: nats://tfbuddy-nats:4222
  TFBUDDY_PROJECT_ALLOW_LIST: tfc-project/
  TFBUDDY_WORKSPACE_ALLOW_LIST: tfc-workspace
  TFBUDDY_DEFAULT_TFC_ORGANIZATION: companyX
```

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
```

TF Buddy uses [doublestar](https://github.com/bmatcuk/doublestar#about) for its path matching. In the example above, the following directories/files would be watched:

* `terraform/$ENV` - anything that is a direct child of `terraform/production` or `terraform/staging`
* `terraform/production/**` - anything that has `terraform/production` as an ancestor
* `terraform/staging/**/*.tf` - any Terraform files that have `terraform/staging` as an ancestor
* `terraform/staging/{foo,bar}/**` - anything that has `terraform/staging/foo` or `terraform/staging/bar` as an ancestor
* `terraform/staging/**/[^0-9]*` - anything that has `terraform/staging` as an ancestor and does _not_ start with an integer

