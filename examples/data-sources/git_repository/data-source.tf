# A terraform { required_providers { git = { source = "UnstoppableMango/git" } } }
# block is required when applying this standalone; see
# examples/provider/provider.tf. It's omitted here because this example
# doubles as an acceptance test run against an in-process build of the
# provider (see internal/provider/examples_test.go).

# Resolves and verifies (via ls-remote) a real, public repository. No auth
# is required for a public, read-only lookup.
data "git_repository" "this" {
  url  = "https://github.com/UnstoppableMango/terraform-provider-git.git"
  host = "github"
}

output "repository_id" {
  value = data.git_repository.this.id
}
