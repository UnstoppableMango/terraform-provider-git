terraform {
  required_providers {
    git = {
      source = "UnstoppableMango/git"
    }
  }
}

provider "git" {
  # "go-git" (default) or "exec".
  git_implementation = "go-git"

  # Optional default auth, used by any resource/data source that doesn't
  # set its own auth.token.
  auth = {
    token = "ghp_..."
  }
}
