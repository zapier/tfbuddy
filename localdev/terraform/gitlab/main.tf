terraform {
  required_providers {
    gitlab = {
      source  = "gitlabhq/gitlab"
      version = "3.20.0"
    }
  }
}

provider "gitlab" {
  # requires GITLAB_TOKEN to be set
}

variable "parent_vars" {
  type = map(string)
  default = {}
}

variable "tfbuddy_gitlab_hook_secret_key" {
  default = "asdf"
}

module "vcs_files" {
  source = "../modules/vcs_files"

  tfc_organization = local.tfc_organization
  tfc_workspace    = local.tfc_workspace
}

locals {
  ngrok_url        = try(var.parent_vars.ngrok_url, "")
  random_pet       = try(var.parent_vars.random_pet, "")
  tfc_organization = try(var.parent_vars.tfc_organization, "")
  tfc_workspace    = try(var.parent_vars.tfc_workspace, "")
}

# Make a backup of the settings provided by parent TF workspace
# If the parent is destroyed it will remove the tfvars file that this
# workspace would need to also do a destroy.
# TF loads the tfvars in alphabetical order, so the parent.auto.tfvars 
# will take precedence.
resource "local_file" "localdev_auto_tfvars" {
  filename = "localdev.auto.tfvars"
  content  = <<EOF
parent_vars=${format("%#v", var.parent_vars)}
EOF
}
