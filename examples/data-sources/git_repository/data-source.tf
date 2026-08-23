# No required_providers block here: this example doubles as an acceptance test
# against an in-process build. See examples/provider/provider.tf for one.

# Checks a public repository is there and reachable, via ls-remote.
data "git_repository" "this" {
  url  = "https://github.com/UnstoppableMango/terraform-provider-git.git"
  host = "github"
}

output "repository_id" {
  value = data.git_repository.this.id
}
