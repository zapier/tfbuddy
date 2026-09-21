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

variable "parent_vars" {
  type    = map(string)
  default = {}
}

locals {
  ngrok_url        = try(var.parent_vars.ngrok_url, "")
  tfc_workspace_id = try(var.parent_vars.tfc_workspace_id, "")
  tfbuddy_base_url = chomp(local.ngrok_url)
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
