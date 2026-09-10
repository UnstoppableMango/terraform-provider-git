# Omits the required_providers block since this example also runs as an
# acceptance test against an in-process build; see examples/provider/provider.tf.

# Resolves and verifies a public repository via ls-remote.
data "git_repository" "this" {
  url  = "https://github.com/UnstoppableMango/terraform-provider-git.git"
  host = "github"
}

# Discovers the repository this configuration is stored in, reading the URL
# from its "origin" remote instead of hardcoding it. `host` is inferred from
# that URL, and `local.head_ref` reports the branch currently checked out.
data "git_repository" "current" {
  # `path` defaults to the directory Terraform was invoked from, and parent
  # directories are walked until a repository is found. Set `path = path.root`
  # when the configuration may be applied from outside its own directory.
  local = {}
}

output "repository_id" {
  value = data.git_repository.this.id
}

output "current_repository_url" {
  value = data.git_repository.current.url
}
