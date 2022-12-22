# Usage 

How to deploy TF Buddy onto your infrastructure. We provide a helm chart to simplify deployment. 

## Dependencies
1. [NATS](https://nats.io/)
1. Gitlab / Github
1. Terraform Cloud Workspace

## Helm Chart

```console
export TFC_TOKEN="" \
       GITLAB_TOKEN="" \
       GITLAB_TOKEN_USER=""

helm install tfbuddy charts/tfbuddy \
  --set secrets.env.TFC_TOKEN="${TFC_TOKEN}" \
  --set secrets.env.GITLAB_TOKEN="${GITLAB_TOKEN}" \
  --set secrets.env.GITLAB_TOKEN_USER="${GITLAB_TOKEN_USER}"
```

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