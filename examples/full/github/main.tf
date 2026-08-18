# git is deliberately not declared here: the acceptance test substitutes
# an in-process build, so a standalone `terraform apply` needs its own
# required_providers entry for git added below (see examples/provider/provider.tf).
terraform {
  required_providers {
    github = {
      source  = "integrations/github"
      version = ">= 6.0.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0.0"
    }
  }
}

variable "github_token" {
  type      = string
  sensitive = true
}

# The github provider picks up its token from the GITHUB_TOKEN environment
# variable; set that before running `terraform apply` against this example.

# A random suffix avoids repository name collisions across repeated runs.
resource "random_pet" "repo" {
  length = 2
}

# github_repository owns the repo's lifecycle; git_repository/git_branch
# below only observe and track it (see docs/DESIGN.md).
resource "github_repository" "example" {
  name        = "terraform-provider-git-example-${random_pet.repo.id}"
  description = "Ephemeral repository created by a terraform-provider-git example/acceptance test."
  visibility  = "public"
  auto_init   = true
}

data "git_repository" "example" {
  url  = github_repository.example.http_clone_url
  host = "github"
  auth = {
    token = var.github_token
  }
}

resource "git_branch" "example" {
  repository = {
    url  = data.git_repository.example.url
    host = "github"
    auth = {
      token = var.github_token
    }
  }

  name     = github_repository.example.default_branch
  base_ref = github_repository.example.default_branch
}

output "repository_url" {
  value = github_repository.example.html_url
}

output "resolved_sha" {
  value = git_branch.example.base_sha
}
