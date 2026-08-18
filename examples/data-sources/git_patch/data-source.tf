# A terraform { required_providers { git = { source = "UnstoppableMango/git" } } }
# block is required when applying this standalone; see
# examples/provider/provider.tf. It's omitted here because this example
# doubles as an acceptance test run against an in-process build of the
# provider (see internal/provider/examples_test.go).

# Resolves a patch from an inline unified diff. No network access needed.
data "git_patch" "from_content" {
  content = <<-EOT
    diff --git a/example.txt b/example.txt
    new file mode 100644
    index 0000000..fe0a02d
    --- /dev/null
    +++ b/example.txt
    @@ -0,0 +1 @@
    +example content
  EOT
}

# Resolves a patch from a local file, read on every refresh. abspath()
# ensures the path is resolved before it reaches the provider, since a
# provider process's own working directory isn't guaranteed to match
# Terraform's.
data "git_patch" "from_file" {
  file = abspath("${path.module}/sample.patch")
}

# Resolves a patch from a real, immutable commit on a public GitHub
# repository. Read-only; no auth is required for a public repo, though
# auth.token can be set to raise API rate limits.
data "git_patch" "from_github" {
  github = {
    repository = "UnstoppableMango/terraform-provider-git"
    commit     = "6239e82d9874271ed9cee8c6d0b881bf0f49ffc6"
  }
}

output "content_patch_id" {
  value = data.git_patch.from_content.id
}

output "file_patch_id" {
  value = data.git_patch.from_file.id
}

output "github_patch_diff" {
  value = data.git_patch.from_github.diff
}
