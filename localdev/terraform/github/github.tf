resource "github_repository" "tfbuddy_test_project" {
  name        = local.random_pet
  description = "TFBuddy test repo"

  visibility = "public"
  auto_init  = true
}

data "github_branch" "main" {
  repository = github_repository.tfbuddy_test_project.name
  branch     = "main"

}

resource "github_repository_webhook" "tfbuddy_localdev_url" {
  repository = github_repository.tfbuddy_test_project.name

  configuration {
    url          = "${local.ngrok_url}/hooks/github/events"
    content_type = "json"
    insecure_ssl = false
    secret       = var.tfbuddy_github_hook_secret_key
  }

  active = true

  events = ["issue_comment", "pull_request"]
}

resource "github_repository_file" "terraform_tf" {
  repository          = github_repository.tfbuddy_test_project.name
  branch              = data.github_branch.main.branch
  file                = "terraform.tf"
  content             = module.vcs_files.files["terraform.tf"]
  commit_message      = "add terraform.tf file"
  commit_author       = "Terraform User"
  commit_email        = "terraform@example.com"
  overwrite_on_create = true
}

resource "github_repository_file" "tfbuddy_yaml" {
  repository          = github_repository.tfbuddy_test_project.name
  branch              = data.github_branch.main.branch
  file                = ".tfbuddy.yaml"
  content             = module.vcs_files.files[".tfbuddy.yaml"]
  commit_message      = "add .tfbuddy.yaml file"
  commit_author       = "Terraform User"
  commit_email        = "terraform@example.com"
  overwrite_on_create = true
}

resource "github_branch" "test_change" {
  repository = github_repository.tfbuddy_test_project.name
  branch     = "test-change"

  depends_on = [github_repository_file.tfbuddy_yaml]
}

resource "github_repository_file" "test_change_branch_main_tf" {
  repository          = github_repository.tfbuddy_test_project.name
  file                = "main.tf"
  branch              = github_branch.test_change.branch
  content             = module.vcs_files.files["main.tf"]
  commit_message      = "add main.tf file"
  commit_author       = "Terraform User"
  commit_email        = "terraform@example.com"
  overwrite_on_create = true
}

resource "github_repository_pull_request" "test_change" {
  base_repository = github_repository.tfbuddy_test_project.name
  base_ref        = "main"
  head_ref        = github_branch.test_change.branch
  title           = "TFBuddy test"
  body            = "This will change everything"
  depends_on      = [github_repository_file.test_change_branch_main_tf]
}

output "github_repo_url" {
  value = <<EOF
Repo: ${github_repository.tfbuddy_test_project.html_url}

PR: https://github.com/${github_repository.tfbuddy_test_project.full_name}/pull/${github_repository_pull_request.test_change.number}
EOF
}

output "github_repo_name" {
  value = github_repository.tfbuddy_test_project.full_name
}