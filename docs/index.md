# TFBuddy - Terraform Cloud workflow engine

## What is TFBuddy

TFBuddy is an application designed to simplify executing Terraform in your pull requests. It supports GitHub, GitLab,
and Terraform Cloud Workspaces currently. TFBuddy has been in use at Zapier, in production since March 2022, and is
still under active development.

## Why TFBuddy?

1. Apply before merging your pull requests
2. Cleaner breakdown of what's going to change in your pull request
3. Keep your TFC workspaces in API-driven / CLI-driven mode so you can still use the `terraform` CLI for maintenance
   actions not easily achieved via GitOps.

## Documentation

### Quick Links

* [Getting Started](usage.md) - Installation and configuration
* [Architecture](architecture.md) - How TFBuddy works
* [Contributing](contributing.md) - Development environment setup

To learn more about TFBuddy [go to the complete documentation](https://tfbuddy.readthedocs.io/).