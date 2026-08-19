# Omits the required_providers block since this example also runs as an
# acceptance test against an in-process build; see examples/provider/provider.tf.

# Tracks the "main" branch of a public repository. Safe to run against a
# repository you don't own; git_branch never pushes.
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
