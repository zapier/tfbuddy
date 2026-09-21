locals {
  child_tfvars = <<EOF
parent_vars = {
  ngrok_url="${var.ngrok_url}",
  random_pet="${random_pet.random_name.id}",
  tfc_organization="${tfe_workspace.test.organization}",
  tfc_workspace="${tfe_workspace.test.name}",
  tfc_workspace_id="${tfe_workspace.test.id}"
}
EOF

  child_tf_dirs = [
    "gitlab",
    "github",
    "notification"
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

# The notification configuration lives in ./notification, which is applied after
# TFBuddy is up. Creating it makes TFC send a verification request that TFBuddy
# has to be serving to answer, and TFBuddy cannot start until this workspace
# exists.

resource "local_file" "child_tfvars" {
  for_each = toset(local.child_tf_dirs)

  filename = "${each.value}/parent.auto.tfvars"
  content  = local.child_tfvars
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
