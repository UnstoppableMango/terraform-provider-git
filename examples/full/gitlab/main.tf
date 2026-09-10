# git's required_providers entry is deliberately not declared here: the
# acceptance test substitutes an in-process build via ProtoV6ProviderFactories
# instead, so a standalone `terraform apply` needs its own required_providers
# entry for git added alongside the provider block below.
terraform {
  required_providers {
    gitlab = {
      source  = "gitlabhq/gitlab"
      version = ">= 17.0.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0.0"
    }
  }
}

# git_branch.example_with_patches below applies its patch stack via
# git.Client.ApplyPatches. This provider block pins the go-git backend
# explicitly (it's also the default) so this example exercises go-git's
# pure-Go patch application, backed by github.com/bluekeyes/go-gitdiff,
# rather than the exec backend's `git apply`/`git commit` shellouts. This
# provider block is declared directly in this example (unlike the other,
# patch-free examples) so both the acceptance test and a standalone
# `terraform apply` exercise the same, explicit backend.
provider "git" {
  git_implementation = "go-git"
}

variable "gitlab_token" {
  type      = string
  sensitive = true
}

# The gitlab provider picks up its token from the GITLAB_TOKEN environment
# variable; set that before running `terraform apply` against this example.

# A random suffix avoids project name collisions across repeated runs.
resource "random_pet" "project" {
  length = 2
}

# gitlab_project owns the project's lifecycle; git_repository/git_branch
# below only observe and track it (see docs/DESIGN.md). initialize_with_readme
# gives it a resolvable default branch immediately, matching how the GitHub
# example's github_repository sets auto_init.
resource "gitlab_project" "example" {
  name                   = "terraform-provider-git-example-${random_pet.project.id}"
  description            = "Ephemeral project created by a terraform-provider-git example/acceptance test."
  visibility_level       = "public"
  initialize_with_readme = true
}

data "git_repository" "example" {
  url  = gitlab_project.example.http_url_to_repo
  host = "gitlab"
  auth = {
    token = var.gitlab_token
  }
}

resource "git_branch" "example" {
  repository = {
    url  = data.git_repository.example.url
    host = "gitlab"
    auth = {
      token = var.gitlab_token
    }
  }

  name     = gitlab_project.example.default_branch
  base_ref = gitlab_project.example.default_branch
}

# Resolves a patch stack entry from an inline unified diff. See
# examples/data-sources/git_patch for other patch sources (file, GitHub
# PR/commit, or GitLab MR/commit).
data "git_patch" "example" {
  content = <<-EOT
    diff --git a/example.txt b/example.txt
    new file mode 100644
    index 0000000..fe0a02d
    --- /dev/null
    +++ b/example.txt
    @@ -0,0 +1 @@
    +Hello from a git_branch patch stack!
  EOT
}

# Unlike git_branch.example above, this branch has a non-empty patch stack:
# on apply, its patches are applied as commits on top of base_ref and
# force-pushed to "feature", since the resulting commits are always rewritten
# from base_ref rather than incrementally amended (see docs/DESIGN.md's
# "Patch semantics" and "Push behavior"). Safe here because the project above
# is ephemeral and owned by this example.
resource "git_branch" "example_with_patches" {
  repository = {
    url  = data.git_repository.example.url
    host = "gitlab"
    auth = {
      token = var.gitlab_token
    }
  }

  name     = "feature"
  base_ref = gitlab_project.example.default_branch

  patches = [data.git_patch.example.diff]
}

output "project_url" {
  value = gitlab_project.example.web_url
}

output "resolved_sha" {
  value = git_branch.example.base_sha
}

output "patched_branch_resolved_ref" {
  value = git_branch.example_with_patches.resolved_ref
}
