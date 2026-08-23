# No required_providers block here: this example doubles as an acceptance test
# against an in-process build. See examples/provider/provider.tf for one.

# Tracks the "main" branch of a public repository. Safe against a repository you
# don't own: with no patches set, git_branch never pushes.
resource "git_branch" "main" {
  repository = {
    url  = "https://github.com/UnstoppableMango/terraform-provider-git.git"
    host = "github"
  }

  name     = "main"
  base_ref = "main"
}

output "resolved_sha" {
  value = git_branch.main.base_sha
}
