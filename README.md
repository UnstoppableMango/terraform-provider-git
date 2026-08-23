# terraform-provider-git

[![CI](https://github.com/UnstoppableMango/terraform-provider-git/actions/workflows/ci.yml/badge.svg)](https://github.com/UnstoppableMango/terraform-provider-git/actions/workflows/ci.yml)
[![Terraform Registry](https://img.shields.io/badge/terraform-registry-844FBA)](https://registry.terraform.io/providers/UnstoppableMango/git/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/UnstoppableMango/terraform-provider-git.svg)](https://pkg.go.dev/github.com/UnstoppableMango/terraform-provider-git)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![built with nix](https://builtwithnix.org/badge.svg)](https://builtwithnix.org)
[![Latest commit](https://img.shields.io/github/last-commit/UnstoppableMango/terraform-provider-git)](https://github.com/UnstoppableMango/terraform-provider-git/commits/main)

Manage a git repository with [Terraform](https://www.terraform.io/): the branches you track, and the patches stacked on top of them.

## Why

Long-lived branches and patch series are tedious to keep by hand.
You rebase, you resolve the same conflict again, you lose track of which patch belongs where.
Write it down in HCL instead and let `terraform apply` do the bookkeeping.

[GOALS.md](GOALS.md) says what this is and isn't trying to be.

## Status

Early. Three things work:

- `git_repository` (data source) points at a repository that already exists and checks it's reachable with `ls-remote`.
- `git_branch` (resource) tracks a branch against a `base_ref`, applies an ordered `patches` stack on top, and force-pushes the result.
- `git_patch` (data source) turns an inline diff, a local file, a GitHub PR or commit, or a GitLab MR or commit into a unified diff plus a content-addressed ID.

[AGENTS.md](AGENTS.md#current-state-vs-design) tracks what's actually built, [docs/DESIGN.md](docs/DESIGN.md) covers the rest of the intended model.

## Usage

Published on the [Terraform Registry](https://registry.terraform.io/providers/UnstoppableMango/git/latest) as `UnstoppableMango/git`.

```hcl
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
```

### Tracking a branch

```hcl
resource "git_branch" "main" {
  repository = {
    url  = "https://github.com/UnstoppableMango/terraform-provider-git.git"
    host = "github"
  }

  name     = "main"
  base_ref = "main"
}
```

### Declaring a patch stack

This is the part worth having: an ordered list of patches applied on top of a tracked branch, quilt-style.
Reorder the list, add an entry, or drop one, and the next `apply` rebuilds the whole stack from `base_ref`.

```hcl
data "git_patch" "from_github_pr" {
  github = {
    repository = "UnstoppableMango/terraform-provider-git"
    pr         = 123
  }
}

resource "git_branch" "feature" {
  repository = { url = "https://github.com/UnstoppableMango/terraform-provider-git.git", host = "github" }
  name       = "feature"
  base_ref   = "main"

  patches = [data.git_patch.from_github_pr.diff]
}
```

[docs/DESIGN.md](docs/DESIGN.md#patch-semantics) has the exact semantics.
[examples/full/github](examples/full/github) and [examples/full/gitlab](examples/full/gitlab) are end-to-end runs you can apply yourself.

## Development

You'll want the [Nix](https://nixos.org/) dev shell; `direnv allow` picks it up from `.envrc`.

```sh
make build   # nix build .#
make test    # go tool ginkgo run -r
make check   # nix flake check (lint)
make fmt     # nix fmt (gofmt, nixfmt, actionlint)
```

To run one package or one spec, call Ginkgo directly:

```sh
go tool ginkgo run ./internal/provider
go tool ginkgo run --focus "<Describe/It text>" ./internal/provider
```

Touched `go.mod`? Run `make tidy` to regenerate the lockfiles.

Architecture notes live in [AGENTS.md](AGENTS.md).

## License

[MIT](LICENSE)
