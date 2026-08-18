# A terraform { required_providers { git = { source = "UnstoppableMango/git" } } }
# block is required when applying this standalone; see
# examples/provider/provider.tf. It's omitted here because this example
# doubles as an acceptance test run against an in-process build of the
# provider (see internal/provider/examples_test.go).

# Tracks the "main" branch of a real, public repository. This never pushes
# to the remote: git_branch only resolves and reports drift against
# base_ref, so it's safe to run against a repository you don't own.
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
