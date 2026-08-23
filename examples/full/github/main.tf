# No required_providers entry for git on purpose: the acceptance test swaps in
# an in-process build via ProtoV6ProviderFactories. If you're running this by
# hand, add one yourself alongside the provider block below.
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

# github_repository owns the repo. git_repository and git_branch below only
# watch and track it, they never create anything (see docs/DESIGN.md).
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

# One stack entry, from an inline unified diff. examples/data-sources/git_patch
# shows the other sources: a file, a GitHub PR, a commit.
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
# the repository above is ephemeral and belongs to this example.
resource "git_branch" "example_with_patches" {
  repository = {
    url  = data.git_repository.example.url
    host = "github"
    auth = {
      token = var.github_token
    }
  }

  name     = "feature"
  base_ref = github_repository.example.default_branch

  patches = [data.git_patch.example.diff]
}

output "repository_url" {
  value = github_repository.example.html_url
}

output "resolved_sha" {
  value = git_branch.example.base_sha
}

output "patched_branch_resolved_ref" {
  value = git_branch.example_with_patches.resolved_ref
}
