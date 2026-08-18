# github and random are real, published providers, installed for real from
# the public registry both here and when this example runs as an
# acceptance test (TestAccExampleGitHubFull in
# internal/provider/examples_test.go). git is deliberately not declared
# here: a standalone `terraform apply` of this example needs its own
#
#   git = {
#     source = "UnstoppableMango/git"
#   }
#
# entry added to required_providers below, but the acceptance test instead
# substitutes an in-process build of this provider, which requires the
# config to have no opinion about git's source.
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

# No explicit provider blocks: git_implementation defaults to "go-git", and
# the github provider picks up its token from the GITHUB_TOKEN environment
# variable (a PAT belonging to the UnstoppableMango account), set that
# before running `terraform apply` against this example.

# A short random suffix keeps repeated runs of this example from colliding
# on repository name.
resource "random_pet" "repo" {
  length = 2
}

# This provider deliberately never creates or deletes repositories on the
# remote host (see docs/DESIGN.md), that's the github provider's job. This
# example demonstrates the intended composition: github_repository owns the
# repo's lifecycle, and git_repository/git_branch observe and track it.
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
