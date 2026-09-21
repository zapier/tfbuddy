
resource "gitlab_project" "tfbuddy_test_project" {
  name = local.random_pet

  # TFBuddy's require-pipeline-success gate defers to this setting, so without
  # it the gate logs a warning and allows the apply through.
  only_allow_merge_if_pipeline_succeeds = true

  # The gate treats a job that is merely pending as blocking, so without a
  # runner the fixture pipeline would refuse apply without ever running.
  shared_runners_enabled = true
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
  encoding       = "base64"
  commit_message = "add terraform.tf file"
}

resource "gitlab_repository_file" "tfbuddy_yaml" {
  project        = gitlab_project.tfbuddy_test_project.id
  file_path      = ".tfbuddy.yaml"
  branch         = "main"
  content        = base64encode(module.vcs_files.files[".tfbuddy.yaml"])
  encoding       = "base64"
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

# Lives on the source branch only: GitLab builds a merge request pipeline from
# the source branch's CI config, and keeping it off main avoids reordering the
# commit chain that gitlab_branch.test_change branches from.
resource "gitlab_repository_file" "gitlab_ci_yml" {
  project        = gitlab_project.tfbuddy_test_project.id
  file_path      = ".gitlab-ci.yml"
  branch         = gitlab_branch.test_change.name
  content        = base64encode(module.vcs_files.files[".gitlab-ci.yml"])
  encoding       = "base64"
  commit_message = "add .gitlab-ci.yml fixture"

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
  encoding       = "base64"
  commit_message = "add main.tf file"
  # Serialized behind the CI fixture: both commit to test-change, and GitLab
  # rejects concurrent commits to the same branch.
  depends_on = [
    gitlab_repository_file.terraform_tf,
    gitlab_repository_file.tfbuddy_yaml,
    gitlab_repository_file.gitlab_ci_yml
  ]
}