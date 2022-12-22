output "gitlab_project_url" {
  value = <<EOF
${gitlab_project.tfbuddy_test_project.web_url}
EOF
}

output "gitlab_project_name" {
  value = gitlab_project.tfbuddy_test_project.path_with_namespace
}