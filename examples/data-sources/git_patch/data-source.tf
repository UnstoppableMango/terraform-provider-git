# Omits the required_providers block since this example also runs as an
# acceptance test against an in-process build; see examples/provider/provider.tf.

# Resolves a patch from an inline unified diff.
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

# Resolves a patch from a local file. abspath() ensures the path resolves
# correctly regardless of the provider process's working directory.
data "git_patch" "from_file" {
  file = abspath("${path.module}/sample.patch")
}

# Resolves a patch from a commit on a public GitHub repository.
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
