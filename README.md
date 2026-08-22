# terraform-provider-git

[![CI](https://github.com/UnstoppableMango/terraform-provider-git/actions/workflows/ci.yml/badge.svg)](https://github.com/UnstoppableMango/terraform-provider-git/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/UnstoppableMango/terraform-provider-git.svg)](https://pkg.go.dev/github.com/UnstoppableMango/terraform-provider-git)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![built with nix](https://builtwithnix.org/badge.svg)](https://builtwithnix.org)
[![Latest commit](https://img.shields.io/github/last-commit/UnstoppableMango/terraform-provider-git)](https://github.com/UnstoppableMango/terraform-provider-git/commits/main)

A [Terraform](https://www.terraform.io/) provider that declares and reconciles the state of a git repository: tracked branches and a quilt-style ordered patch stack applied on top of them.

## Why

Instead of hand-maintaining long-lived feature branches or juggling patch series by hand, the desired state lives in HCL and Terraform reconciles the repository to match.
See [GOALS.md](GOALS.md) for the full vision and non-goals.

## Status

Early and incomplete. Implemented so far:

- `git_repository` (data source) — resolves and verifies an existing repository via `ls-remote`.
- `git_branch` (resource) — tracks a branch against a `base_ref`, applies an ordered `patches` stack on top of it, and force-pushes the result.
- `git_patch` (data source) — resolves a unified diff and content-addressed ID from inline content, a local file, or a GitHub PR/commit.

Not yet implemented: the GitLab hosting-API backend and generated Registry docs.
See [AGENTS.md](AGENTS.md#current-state-vs-design) for the up-to-date breakdown and [docs/DESIGN.md](docs/DESIGN.md) for the full resource model.

## Usage

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

This is the provider's core value-add: an ordered stack of patches, declared in HCL, applied on top of a tracked branch, quilt-style.
Reordering, adding, or removing entries in `patches` rewrites the stack from `base_ref` on the next `apply`.

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

See [docs/DESIGN.md](docs/DESIGN.md#patch-semantics) for the full semantics, and [examples/full/github](examples/full/github) for a complete end-to-end run.

## Development

Requires the [Nix](https://nixos.org/) dev shell (`direnv allow` picks it up automatically via `.envrc`).

```sh
make build   # nix build .#
make test    # go tool ginkgo run -r
make check   # nix flake check (lint)
make fmt     # nix fmt (gofmt, nixfmt, actionlint)
```

Run a single package or spec with Ginkgo directly:

```sh
go tool ginkgo run ./internal/provider
go tool ginkgo run --focus "<Describe/It text>" ./internal/provider
```

After touching `go.mod`, regenerate lockfiles with `make tidy`.

See [AGENTS.md](AGENTS.md) for architecture notes.

## License

[MIT](LICENSE)
