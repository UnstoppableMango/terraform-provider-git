# Design

Details behind [GOALS.md](../GOALS.md).

## Terminology

- **Tracked branch**: a branch whose tip commit Terraform observes and advances.
- **Patch stack**: an ordered series of patches applied on top of a tracked branch's base ref, in the spirit of `quilt push` / `quilt pop`.
- **Base ref**: the upstream commit a branch is tracking before any patches are applied.
- **Resolved ref**: the commit produced after applying the full patch stack on top of the base ref.

## Resource model

Three resource types, each with an independent lifecycle:

1. `git_repository` - references an existing repository. Reference-only: this provider never creates or deletes repositories on the host. It resolves connection details (URL, host type, auth) used by other resources.
2. `git_branch` - tracks a branch within a `git_repository`. Declares the base ref to follow and the ordered list of `git_patch` resources that make up its stack.
3. `git_patch` - a single patch with its own lifecycle, referenced by ID from a `git_branch`'s ordered patch list. A patch can come from a local file, inline diff content, or a remote source (e.g. a GitHub commit/PR).

Patch order is an explicit ordered list on `git_branch` (analogous to a quilt series file), not inferred from a dependency graph.

## Access backends

Repository access is pluggable behind a common interface:

- **Local clone** (default): the provider clones/fetches the repository to a local workdir and operates on it with real git, then pushes results.
- **Hosting API**: for hosts/operations better served by REST/GraphQL (e.g. GitHub, GitLab), the provider can act through their APIs instead of a local clone.

Which backend is used may vary per operation and per host; the goal is to pick whichever is correct and efficient for the operation, not to force one strategy everywhere.

The local clone backend's git implementation is itself pluggable behind a common interface, defaulting to go-git (pure Go, no external dependency) and overridable to shell out to the git binary where go-git's behavior isn't sufficient (e.g. certain `git apply`/`git am` edge cases, credential helpers).

## Auth

Credentials can be supplied at the provider level (a default applied to all resources) and overridden per-resource, for configs that span multiple hosts or accounts.
Initial hosts: GitHub and GitLab.

## Patch semantics

Patches are applied as real commits on top of the tracked branch's base ref, not as uncommitted working-tree changes.
Applying, reordering, or removing patches rewrites those commits, matching quilt's model of a mutable stack sitting on top of a stable base.

## Read behavior

Read (refresh) actions update the ref recorded in state to reflect what's actually on the remote: the base ref for `git_branch`, and the resolved ref after the patch stack for the branch as a whole.
This is how drift (someone pushing directly, force-pushing, etc.) becomes visible to `terraform plan`.

## Conflict handling

When the patch stack no longer applies cleanly (base ref moved upstream, a patch conflicts), behavior is configurable per `git_branch`:

- **Fail**: apply errors out with conflict details; the user resolves manually and retries.
- **Force**: the provider resets the branch to the tracked base ref and reapplies the full patch stack from scratch, discarding drift, to guarantee the declared state wins.

## Push behavior

After reconciling the patch stack, `git_branch` pushes the resulting branch to the remote.
Because the stack is rewritten on each apply, this is a force-push.

## Open questions

- Exact shape of the `git_patch` remote-source attribute (how a GitHub commit/PR reference is expressed).
- Import behavior for existing branches that already have divergent commits on top of their base ref.
- Whether local-clone workdirs persist between applies or are ephemeral per-run.
