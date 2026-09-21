# Terraform Cloud sends a verification request to `url` as soon as this resource
# is created, so it has to be applied after TFBuddy is serving. That is why it
# lives in its own root module instead of alongside the workspace: the workspace
# has to exist before TFBuddy can be configured, and TFBuddy has to be up before
# this can be created.
resource "tfe_notification_configuration" "test" {
  count = length(local.ngrok_url) > 0 && length(local.tfc_workspace_id) > 0 ? 1 : 0

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
  workspace_id = local.tfc_workspace_id
}
