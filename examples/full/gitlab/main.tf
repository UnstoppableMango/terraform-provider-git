# No required_providers entry for git on purpose: the acceptance test swaps in
# an in-process build via ProtoV6ProviderFactories. If you're running this by
# hand, add one yourself alongside the provider block below.
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

# git_branch.example_with_patches below applies its patch stack through
# git.Client.ApplyPatches. Pinning go-git here (it's the default anyway) means
# this example goes through go-git's pure-Go patch application, backed by
# github.com/bluekeyes/go-gitdiff, instead of the exec backend shelling out to
# `git apply` and `git commit`. The pin is spelled out in the example, unlike
# the patch-free ones, so the acceptance test and a hand-run `terraform apply`
# both hit the same backend.
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

# gitlab_project owns the project. git_repository and git_branch below only
# watch and track it (see docs/DESIGN.md). initialize_with_readme gives it a
# resolvable default branch right away, the same trick auto_init pulls in the
# GitHub example.
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

# One stack entry, from an inline unified diff. examples/data-sources/git_patch
# shows the other sources: a file, a GitHub PR or commit, a GitLab MR or commit.
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

# This branch, unlike git_branch.example above, has a patch stack. On apply the
# patches land as commits on top of base_ref and get force-pushed to "feature",
# because the stack is always rebuilt from base_ref rather than amended in place
# (see "Patch semantics" and "Push behavior" in docs/DESIGN.md). Fine to do here:
# the project above is ephemeral and belongs to this example.
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
