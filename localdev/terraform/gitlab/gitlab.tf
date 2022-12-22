
resource "gitlab_project" "tfbuddy_test_project" {
  name = local.random_pet
}

# Add a hook to the project
resource "gitlab_project_hook" "tfbuddy_localdev_url" {
  count = length(local.ngrok_url) > 0 ? 1 : 0

  project               = gitlab_project.tfbuddy_test_project.id
  url                   = "${local.ngrok_url}/hooks/gitlab/project"
  merge_requests_events = true
  push_events           = true
  note_events           = true

  token = var.tfbuddy_gitlab_hook_secret_key
}

resource "gitlab_repository_file" "terraform_tf" {
  project        = gitlab_project.tfbuddy_test_project.id
  file_path      = "terraform.tf"
  branch         = "main"
  content        = base64encode(module.vcs_files.files["terraform.tf"])
  commit_message = "add terraform.tf file"
}

resource "gitlab_repository_file" "tfbuddy_yaml" {
  project        = gitlab_project.tfbuddy_test_project.id
  file_path      = ".tfbuddy.yaml"
  branch         = "main"
  content        = base64encode(module.vcs_files.files[".tfbuddy.yaml"])
  commit_message = "add .tfbuddy.yaml file"

  depends_on = [gitlab_repository_file.terraform_tf]
}

resource "gitlab_branch" "test_change" {
  name    = "test-change"
  ref     = gitlab_repository_file.tfbuddy_yaml.commit_id
  project = gitlab_project.tfbuddy_test_project.id

  depends_on = [
    gitlab_repository_file.terraform_tf,
    gitlab_repository_file.tfbuddy_yaml
  ]
}

resource "gitlab_repository_file" "main_tf" {
  project        = gitlab_project.tfbuddy_test_project.id
  file_path      = "main.tf"
  branch         = gitlab_branch.test_change.name
  content        = base64encode(module.vcs_files.files["main.tf"])
  commit_message = "add main.tf file"
  depends_on = [
    gitlab_repository_file.terraform_tf,
    gitlab_repository_file.tfbuddy_yaml
  ]
}