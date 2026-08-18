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
}
