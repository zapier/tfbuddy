terraform {
  required_providers {
    gitlab = {
      source  = "gitlabhq/gitlab"
      version = "3.20.0"
    }
    tfe = {
      source  = "hashicorp/tfe"
      version = "0.31.0"
    }
  }
}

variable "ngrok_url" {
  type = string
}

variable "tfc_organization" {
  type = string
}

variable "tfbuddy_gitlab_hook_secret_key" {
  type      = string
  sensitive = true
}

variable "enable_webhooks" {
  description = "Create webhooks only after the local TFBuddy endpoint is ready."
  type        = bool
  default     = false
}

locals {
  workspace_names = [
    for index in range(2) : "${random_pet.fixture.id}-${index + 1}"
  ]
  delayed_workspace_name = "${random_pet.fixture.id}-delayed"

  workspaces = concat(
    [
      for index, name in local.workspace_names : {
        name         = name
        organization = var.tfc_organization
        mode         = "apply-before-merge"
        autoMerge    = true
        dir          = "workspace-${index + 1}"
        triggerDirs  = ["workspace-${index + 1}"]
      }
    ],
    [{
      name         = local.delayed_workspace_name
      organization = var.tfc_organization
      mode         = "apply-before-merge"
      autoMerge    = true
      dir          = "workspace-delayed"
      triggerDirs  = ["workspace-delayed"]
    }],
  )

  repository_files = merge(
    {
      ".tfbuddy.yaml" = yamlencode({ workspaces = local.workspaces })
    },
    {
      for index, name in local.workspace_names :
      "workspace-${index + 1}/terraform.tf" => <<-EOF
        terraform {
          backend "remote" {
            organization = "${var.tfc_organization}"

            workspaces {
              name = "${name}"
            }
          }
        }
      EOF
    },
    {
      for index, _ in local.workspace_names :
      "workspace-${index + 1}/main.tf" => <<-EOF
        resource "random_pet" "fixture" {
          length = 2
        }
      EOF
    },
    {
      "workspace-delayed/terraform.tf" = <<-EOF
        terraform {
          backend "remote" {
            organization = "${var.tfc_organization}"

            workspaces {
              name = "${local.delayed_workspace_name}"
            }
          }
        }
      EOF
      "workspace-delayed/main.tf"      = <<-EOF
        resource "random_pet" "fixture" {
          length = 2
        }
      EOF
    },
  )
}

resource "random_pet" "fixture" {
  prefix = "tfbuddy-automerge"
  length = 2
}

resource "tfe_workspace" "fixture" {
  count = 2

  name               = local.workspace_names[count.index]
  organization       = var.tfc_organization
  allow_destroy_plan = true
}

resource "tfe_notification_configuration" "fixture" {
  count = var.enable_webhooks ? 2 : 0

  name             = "tfbuddy-localdev"
  enabled          = true
  destination_type = "generic"
  triggers = [
    "run:created",
    "run:planning",
    "run:errored",
    "run:needs_attention",
    "run:applying",
    "run:completed",
  ]
  url          = "${trimsuffix(var.ngrok_url, "/")}/hooks/tfc/notification"
  workspace_id = tfe_workspace.fixture[count.index].id
}

resource "gitlab_project" "fixture" {
  name                   = random_pet.fixture.id
  initialize_with_readme = true
}

resource "gitlab_project_hook" "fixture" {
  count = var.enable_webhooks ? 1 : 0

  project               = gitlab_project.fixture.id
  url                   = "${trimsuffix(var.ngrok_url, "/")}/hooks/gitlab/project"
  merge_requests_events = true
  push_events           = true
  note_events           = true
  token                 = var.tfbuddy_gitlab_hook_secret_key
}

resource "gitlab_repository_file" "base" {
  for_each = local.repository_files

  project        = gitlab_project.fixture.id
  file_path      = each.key
  branch         = "main"
  content        = base64encode(each.value)
  commit_message = "add ${each.key}"
}

resource "gitlab_branch" "test_change" {
  name    = "test-change"
  ref     = "main"
  project = gitlab_project.fixture.id

  depends_on = [gitlab_repository_file.base]
}

resource "gitlab_repository_file" "test_change" {
  for_each = toset(["1", "2"])

  project        = gitlab_project.fixture.id
  file_path      = "workspace-${each.value}/change.tf"
  branch         = gitlab_branch.test_change.name
  content        = base64encode("resource \"random_uuid\" \"change\" {}")
  commit_message = "change workspace ${each.value}"
}

resource "gitlab_branch" "missing_workspace_test" {
  name    = "test-missing-workspace"
  ref     = "main"
  project = gitlab_project.fixture.id

  depends_on = [gitlab_repository_file.base]
}

resource "gitlab_repository_file" "missing_workspace_test" {
  for_each = {
    existing = "workspace-1"
    delayed  = "workspace-delayed"
  }

  project        = gitlab_project.fixture.id
  file_path      = "${each.value}/bootstrap-change.tf"
  branch         = gitlab_branch.missing_workspace_test.name
  content        = base64encode("resource \"random_uuid\" \"bootstrap_change\" {}")
  commit_message = "change ${each.value}"
}

output "gitlab_project_name" {
  value = gitlab_project.fixture.path_with_namespace
}

output "gitlab_project_url" {
  value = gitlab_project.fixture.web_url
}

output "tfc_workspaces" {
  value = concat(local.workspace_names, [local.delayed_workspace_name])
}

output "tfc_workspace_urls" {
  value = [
    for workspace in tfe_workspace.fixture :
    "https://app.terraform.io/app/${workspace.organization}/workspaces/${workspace.name}"
  ]
}

output "delayed_workspace_name" {
  value = local.delayed_workspace_name
}
