terraform {
  required_providers {
    tfe = {
      source  = "hashicorp/tfe"
      version = "0.31.0"
    }
  }
}

variable "workspace_name" {
  type = string
}

variable "tfc_organization" {
  type = string
}

variable "ngrok_url" {
  type = string
}

resource "tfe_workspace" "delayed" {
  name               = var.workspace_name
  organization       = var.tfc_organization
  allow_destroy_plan = true
}

resource "tfe_notification_configuration" "delayed" {
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
  workspace_id = tfe_workspace.delayed.id
}
