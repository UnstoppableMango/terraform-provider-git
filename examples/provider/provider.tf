terraform {
  required_providers {
    git = {
      source = "UnstoppableMango/git"
    }
  }
}

provider "git" {
  # One of "go-git" (default, pure-Go implementation) or "exec" (shells out
  # to a git binary on PATH).
  git_implementation = "go-git"
}
