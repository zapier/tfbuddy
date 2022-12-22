locals {
  tfbuddy_base_url = "${chomp(var.ngrok_url)}"

  child_tfvars = <<EOF
parent_vars = {
  ngrok_url="${var.ngrok_url}",
  random_pet="${random_pet.random_name.id}",
  tfc_organization="${tfe_workspace.test.organization}",
  tfc_workspace="${tfe_workspace.test.name}"
}
EOF

  child_tf_dirs = [
    "gitlab",
    "github"
  ]
}

resource "random_pet" "random_name" {
  separator = "-"
  length    = 2
}

output "random_pet" {
  value = random_pet.random_name.id
}

resource "tfe_workspace" "test" {
  name         = random_pet.random_name.id
  organization = var.tfc_organization

  allow_destroy_plan  = true
  speculative_enabled = true

}

output "tfc_workspace" {
  value = tfe_workspace.test.name
}

output "tfc_workspace_url" {
  value = <<EOF
https://app.terraform.io/app/${tfe_workspace.test.organization}/workspaces/${tfe_workspace.test.name}
EOF
}

resource "tfe_notification_configuration" "test" {
  count = length(var.ngrok_url) > 0 ? 1 : 0

  name             = "tfbuddy-localdev"
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

  url          = "${local.tfbuddy_base_url}/hooks/tfc/notification"
  workspace_id = tfe_workspace.test.id
}

resource "local_file" "child_tfvars" {
  for_each = toset(local.child_tf_dirs)

  filename = "${each.value}/parent.auto.tfvars"
  content = local.child_tfvars
}


terraform {
  required_providers {
    tfe = {
      source  = "hashicorp/tfe"
      version = "0.31.0"
    }
  }
}

provider "tfe" {
# requires TFC_TOKEN variable is set
}
