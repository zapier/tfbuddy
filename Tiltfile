# -*- mode: Python -*-
load('ext://configmap', 'configmap_from_dict')
load('ext://dotenv', 'dotenv')
load('ext://tests/golang', 'test_go')
load('ext://list_port_forwards', 'display_port_forwards')
load('ext://namespace', 'namespace_yaml')
load('ext://restart_process', 'docker_build_with_restart')
load('ext://secret', 'secret_from_dict')
load('ext://uibutton', 'cmd_button')
load('./.tilt/terraform/Tiltfile', 'local_terraform_resource')
dotenv()

config.define_bool("enable_gitlab")
config.define_bool("enable_github")
config.define_bool("live_debug")
cfg = config.parse()

allow_k8s_contexts([
  'kind-kind',
  'docker-desktop',
  'minikube'
])

k8s_namespace='tfbuddy-localdev'
k8s_yaml(namespace_yaml(k8s_namespace), allow_duplicates=False)
k8s_resource(
  objects=['tfbuddy-localdev:namespace'],
  labels=["localdev"],
  new_name='k8s:namespace'
)
k8s_context=k8s_context()

# Load NGROK Tiltfile
load('./localdev/ngrok/Tiltfile', 'get_ngrok_url')

def checkEnvSet(key):
  if not os.getenv(key):
    fail("{} not set".format(key))

# /////////////////////////////////////////////////////////////////////////////
# T F C  W O R K S P A C E
# /////////////////////////////////////////////////////////////////////////////
checkEnvSet("TFC_ORGANIZATION")
checkEnvSet("TFC_TOKEN")

ngrok_url=get_ngrok_url()
org=str(os.getenv('TFC_ORGANIZATION'))

tfcOutputs=local_terraform_resource(
  'tf-tfc',
  dir='./localdev/terraform',
  env={
    'TF_VAR_ngrok_url': ngrok_url,
    'TFC_TOKEN': os.getenv('TFC_TOKEN'),
    'TF_VAR_tfc_organization': org,
  },
  deps=[
    'localdev/terraform/*.tf',
  ],
  labels=["tfc"],
  resource_deps=['wait-ngrok-url']
)

if tfcOutputs:
  local_resource(
    "tfc-url",
    'echo ""',
    links=link(tfcOutputs['tfc_workspace_url'].rstrip('\n')),
    labels=["tfc"]
  )

# /////////////////////////////////////////////////////////////////////////////
# G I T L A B  P R O J E C T
# /////////////////////////////////////////////////////////////////////////////

if cfg.get('enable_gitlab'):
  checkEnvSet("GITLAB_TOKEN")
  
  gitlabOutputs=local_terraform_resource(
    'tf-gitlab',
    dir='./localdev/terraform/gitlab',
    env={
      'GITLAB_TOKEN': os.getenv('GITLAB_TOKEN'),
      'TF_VAR_tfbuddy_gitlab_hook_secret_key': str(os.getenv('TFBUDDY_HOOK_SECRET')),
    },
    deps=[
      './localdev/terraform/*.tf',
      './localdev/terraform/terraform.tfstate',
      './localdev/terraform/gitlab/*.tf',
    ],
    resource_deps=[
      'tf-tfc',
      'wait-ngrok-url',
    ],
    labels=['gitlab']
  )

# /////////////////////////////////////////////////////////////////////////////
# G I T H U B  P R O J E C T
# /////////////////////////////////////////////////////////////////////////////

if cfg.get('enable_github'):
  checkEnvSet("GITHUB_TOKEN")

  githubOutputs=local_terraform_resource(
    'tf-github',
    dir='./localdev/terraform/github',
    env={
      # 'TF_LOG': 'DEBUG',
      'GITHUB_TOKEN': os.getenv('GITHUB_TOKEN'),
      'TF_VAR_tfbuddy_github_hook_secret_key': str(os.getenv('TFBUDDY_HOOK_SECRET')),
    },
    deps=[
      './localdev/terraform/*.tf',
      './localdev/terraform/terraform.tfstate',
      './localdev/terraform/github/*.tf',
    ],
    resource_deps=[
      'tf-tfc',
      'wait-ngrok-url',
    ],
    labels=['github']
  )

# /////////////////////////////////////////////////////////////////////////////
# N A T S
# /////////////////////////////////////////////////////////////////////////////

k8s_yaml(helm(
  './localdev/nats/chart/nats',
  name='nats',
  values='./localdev/nats/values.yaml',
))
k8s_resource(
  'nats',
  objects=[
    'nats:poddisruptionbudget',
    'nats-config:configmap',
    'nats:serviceaccount'
  ],
  labels=['nats'],
  resource_deps=['k8s:namespace'],
  port_forwards=['4222']
)
k8s_resource(
  'nats-box',
  labels=['nats'],
  resource_deps=['k8s:namespace']
)

natsbox_cmd_prefix=['kubectl', 'exec', 'deploy/nats-box', '--', ]
cmd_button(
  'nats:ls stream',
  argv=natsbox_cmd_prefix + ['nats', 'stream', 'ls'],
  resource='nats-box',
  icon_name='download',
  text='list streams',
)
cmd_button(
  'nats:ls kv',
  argv=natsbox_cmd_prefix + ['nats', 'kv', 'ls'],
  resource='nats-box',
  icon_name='download',
  text='list kvs',
)

# /////////////////////////////////////////////////////////////////////////////
# T F B U D D Y
# /////////////////////////////////////////////////////////////////////////////

test_go(
  'go-test', '.', '.', 
  recursive=True,
  timeout='30s',
  extra_args=['-v'],
  labels=["tfbuddy"]
)

build_cmd='CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -gcflags="all=-N -l" -o build/tfbuddy ./'
local_resource(
  'go-build',
  build_cmd,
  deps=[
    './main.go',
    './go.mod',
    './go.sum',
    './cmd',
    './internal',
    './pkg'
  ],
  labels=["tfbuddy"],
  resource_deps = ['go-test']
)


if cfg.get("live_debug"):
  docker_build_with_restart(
    'tfbuddy-server',
    '.',
    dockerfile='localdev/Dockerfile.dlv',
    entrypoint='$GOPATH/bin/dlv --listen=:2345 --api-version=2 --headless=true --accept-multiclient exec --continue /app/tfbuddy tfc handler',
    ignore=['./Dockerfile', '.git'],
    only=[
      './build',
      # './cmd',
      # './internal',
      # './pkg',
      # './main.go',
      # './go.mod',
      # 'go.sum',
    ],
    live_update=[
        sync('./build/tfbuddy', '/app/tfbuddy'),
        # run(build_cmd),
    ]
  )

else:
  docker_build(
    'tfbuddy-server',
    '.',
    dockerfile='localdev/Dockerfile',
    only=[
      './build',
    ]
  )


cmd_button('loc:go mod tidy',
  argv=['go', 'mod', 'tidy'],
  resource='tfbuddy',
  icon_name='move_up',
  text='go mod tidy',
)
cmd_button('generate-mocks',
   argv=['go', 'generate', './...'],
   resource='tfbuddy',
   icon_name='change_circle',
   text='go generate',
)
cmd_button('restart-pod',
   argv=['kubectl', 'rollout', 'restart', 'deployment/tfbuddy'],
   resource='tfbuddy',
   icon_name='change_circle',
   text='restart pod',
)


# build TFBuddy ConfigMap based on enabled VCS and current state
cfgInputs = {
  'TFBUDDY_LOG_LEVEL': 'debug',
  'TFBUDDY_DEFAULT_TFC_ORGANIZATION' : os.getenv('TFC_ORGANIZATION'),
  'TFBUDDY_WORKSPACE_ALLOW_LIST' : tfcOutputs.setdefault('tfc_workspace', '') if tfcOutputs else '',
  'GITLAB_TOKEN' : os.getenv('GITLAB_TOKEN'),
}
if cfg.get('enable_gitlab') and gitlabOutputs:
  cfgInputs.update({'TFBUDDY_PROJECT_ALLOW_LIST': gitlabOutputs.setdefault('gitlab_project_name', '')})

if cfg.get('enable_github') and githubOutputs:
  cfgInputs.update({'TFBUDDY_GITHUB_REPO_ALLOW_LIST': githubOutputs.setdefault('github_repo_name', '')})

print(cfgInputs)
k8s_yaml(
  configmap_from_dict("tfbuddy-config", inputs=cfgInputs)
)
k8s_resource(
  objects=['tfbuddy-config:configmap'],
  labels=["tfbuddy"],
  new_name='tfbuddy-config',
  resource_deps=['tf-gitlab']
)
k8s_yaml(
  secret_from_dict("tfbuddy-secrets", inputs = {
    'GITHUB_TOKEN' : os.getenv('GITHUB_TOKEN'),
    'GITLAB_TOKEN' : os.getenv('GITLAB_TOKEN'),
    'TFC_TOKEN' : os.getenv('TFC_TOKEN'),
    'TFBUDDY_GITHUB_HOOK_SECRET_KEY' : os.getenv('TFBUDDY_HOOK_SECRET'),
    'TFBUDDY_GITLAB_HOOK_SECRET_KEY' : os.getenv('TFBUDDY_HOOK_SECRET'),
  })
)
k8s_resource(
  objects=['tfbuddy-secrets:secret'],
  labels=["tfbuddy"],
  new_name='tfbuddy-secrets'
)

k8s_yaml(kustomize('localdev/manifests/'))

k8s_resource(
  'tfbuddy',
  port_forwards=[2345, 8080],
  resource_deps=[
    # 'go-build',
    'go-test',
    'nats',
    'tfbuddy-config',
    'tfbuddy-secrets',
  ],
  labels=["tfbuddy"]
)

display_port_forwards()