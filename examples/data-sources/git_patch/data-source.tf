# No required_providers block here: this example doubles as an acceptance test
# against an in-process build. See examples/provider/provider.tf for one.

# From an inline unified diff.
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

# From a local file. abspath() keeps the path correct no matter what working
# directory the provider process happens to be in.
data "git_patch" "from_file" {
  file = abspath("${path.module}/sample.patch")
}

# From a commit on a public GitHub repository.
data "git_patch" "from_github" {
  github = {
    repository = "UnstoppableMango/terraform-provider-git"
    commit     = "6239e82d9874271ed9cee8c6d0b881bf0f49ffc6"
  }
}

# From a commit on a public GitLab project. gitlab-org/gitlab-test is GitLab's
# own fixture project, which exists precisely so tests and examples like this
# one have something stable to point at.
data "git_patch" "from_gitlab" {
  gitlab = {
    project = "gitlab-org/gitlab-test"
    commit  = "2d1db523e11e777e49377cfb22d368deec3f0793"
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

output "gitlab_patch_diff" {
  value = data.git_patch.from_gitlab.diff
}
