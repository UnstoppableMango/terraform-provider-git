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
  # set its own auth.token/auth.ssh.
  auth = {
    token = "ghp_..."

    # SSH auth (for ssh:// or git@host: repository URLs) is an alternative
    # to token, not a combination with it. Provide either an inline private
    # key...
    # ssh = {
    #   private_key = file("~/.ssh/id_ed25519")
    #   # passphrase = "..." # go-git only; the exec implementation errors
    #   # if set, since it can't supply it non-interactively.
    # }

    # ...or a path to one on disk...
    # ssh = {
    #   private_key_path = "~/.ssh/id_ed25519"
    # }

    # ...or, leaving both private_key and private_key_path unset, authenticate
    # via a locally running SSH agent instead.
    # ssh = {}
  }
}
