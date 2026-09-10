---
page_title: "The Current Repository"
subcategory: ""
description: |-
  How git_repository discovers the repository a configuration is stored in, and how to manage another branch of it.
---

# The Current Repository

A configuration stored inside the repository it manages does not have to hardcode its own URL.
Setting `local` on `git_repository` discovers the repository from a checkout on disk, reading the URL from one of its git remotes.

```hcl
data "git_repository" "current" {
  local = {}
}
```

That resolves:

- `url`, from the `origin` remote of the repository containing the directory Terraform was invoked from.
- `host`, inferred from `url`'s hostname (`github`, `gitlab`, or `generic`) unless the configuration sets it.
- `local.head_ref` and `local.head_sha`, the branch and commit currently checked out.
- `local.root`, the absolute path of the working tree's root.
- `local.remote_url`, the URL exactly as git config records it, which differs from `url` only when an SSH remote was rewritten (see below).

`local.path` selects which checkout to discover; parent directories are walked until a repository is found, so any path inside the working copy will do.
It defaults to the directory Terraform was invoked from, which is usually the root module.
Set `path = path.root` when a configuration may be applied from somewhere else, and `path = path.module` to discover the repository a module's own files live in.
`local.remote` selects which remote to read, defaulting to `origin`.

Discovery is a local, read-only operation: it reads git config and `HEAD`, never the network, and never modifies the checkout.
Everything after discovery works exactly as it does with a literal `url`.

## Managing another branch of the same repository

The common case is a repository whose Terraform code lives on `main` and manages a second branch, such as the branch GitHub Pages publishes from.

```hcl
data "git_repository" "current" {
  local = {}
}

data "git_patch" "site" {
  file = "${data.git_repository.current.local.root}/patches/site.diff"
}

resource "git_branch" "pages" {
  repository = {
    url  = data.git_repository.current.url
    host = data.git_repository.current.host

    # Advisory: lets the provider warn if this resource would rewrite the
    # branch the run is checked out on.
    head_ref = data.git_repository.current.local.head_ref
  }

  name     = "gh-pages"
  base_ref = "main"

  patches = [data.git_patch.site.diff]
}
```

A `git_branch` with no `patches` never pushes, so the patch stack is what brings the branch into existence and keeps it up to date.

## SSH remotes

Local checkouts commonly use an SSH remote, such as `git@github.com:owner/repo.git`.
Token authentication needs an `https` URL, so when a token is available (`auth.token` on the data source, or the provider-level default), discovery rewrites scp-style and `ssh://` remote URLs to their `https` equivalent and reports the result as `url`.
`local.remote_url` still shows the original, so the rewrite is visible in state rather than silent.

With no token configured, the URL is passed through unchanged.
The `exec` backend can then reach it over SSH using the host's own credentials; the default `go-git` backend cannot, and reports the repository as unreachable.

## Pushing to the branch the run is on

Pointing a `git_branch` at the branch the Terraform code itself lives on is legal but worth knowing about:

- The local checkout is behind the remote once the apply finishes, since the provider force-pushed commits it does not have.
- Any CI triggered by pushes to that branch runs again on the result. If that CI is what ran Terraform, it will keep triggering itself.

Wiring `repository.head_ref` to `data.git_repository.<name>.local.head_ref` makes the provider warn when this is about to happen.
The warning is advisory: it never changes which branch is pushed.

In CI, note that `HEAD` is often detached: `actions/checkout` checks out a merge commit for `pull_request` events, for example.
`local.head_ref` is empty in that case, and no warning can be raised.

## Further reading

- [`docs/DESIGN.md`](https://github.com/UnstoppableMango/terraform-provider-git/blob/main/docs/DESIGN.md) covers the full resource model.
- [Patch Stacks](./patch-stack.md) covers how `patches` and `on_conflict` behave.
