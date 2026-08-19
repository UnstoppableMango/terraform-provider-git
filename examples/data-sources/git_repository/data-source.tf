# Omits the required_providers block since this example also runs as an
# acceptance test against an in-process build; see examples/provider/provider.tf.

# Resolves and verifies a public repository via ls-remote.
data "git_repository" "this" {
  url  = "https://github.com/UnstoppableMango/terraform-provider-git.git"
  host = "github"
}

output "repository_id" {
  value = data.git_repository.this.id
}
