terraform {
  required_providers {
    github = {
      source  = "integrations/github"
      version = "5.1.0"
    }
  }
}

provider "github" {
  # requires GITHUB_TOKEN env var be set
}

variable "parent_vars" {
  type = map(string)
}

variable "tfbuddy_github_hook_secret_key" {
  default = "asdf"
}

module "vcs_files" {
  source = "../modules/vcs_files"

  tfc_organization = local.tfc_organization
  tfc_workspace    = local.tfc_workspace
}

locals {
  ngrok_url        = var.parent_vars.ngrok_url
  random_pet       = var.parent_vars.random_pet
  tfc_organization = var.parent_vars.tfc_organization
  tfc_workspace    = var.parent_vars.tfc_workspace
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
