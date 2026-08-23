# Design

The details behind [GOALS.md](../GOALS.md).

## Terminology

- **Tracked branch**: a branch whose tip Terraform watches and moves.
- **Patch stack**: an ordered series of patches applied on top of a tracked branch's base ref, like `quilt push` / `quilt pop`.
- **Base ref**: the upstream commit a branch tracks, before any patches.
- **Resolved ref**: the commit you end up with once the whole stack has been applied.

## Resource model

`git_repository` is a data source, not a resource.
It points at a repository that already exists and works out the connection details (URL, host type, auth) that other resources need, but it owns no lifecycle.
Creating and deleting repositories on the host is somebody else's job.

Built on top of that:

1. `git_branch` is a real resource with its own lifecycle. It tracks a branch inside a `git_repository`: the base ref it follows, plus the ordered patches that make up its stack.
1. `git_patch` is another data source. It resolves what a single patch is and what's in it, from a local file, inline diff content, or a remote source such as a GitHub commit or PR. Reading a patch is deliberately separate from applying one: applying, committing, and pushing all belong to `git_branch`, which references patches by ID from its ordered list.

Patch order comes from an explicit ordered list on `git_branch`, the equivalent of quilt's series file.
Nothing is inferred from a dependency graph.

## Access backends

Repository access sits behind a common interface, with more than one implementation.

- **Local clone** (default): clone or fetch the repository into a local workdir, do the work there with real git, push the result.
- **Hosting API**: for hosts and operations where REST or GraphQL is a better fit (GitHub, GitLab), talk to the API instead of cloning.

Which one gets used can vary per host and per operation.
The point is to pick whatever is correct and cheap for the job at hand, not to commit to one strategy everywhere.

Inside the local clone backend, the git implementation is pluggable too.
It defaults to go-git, which is pure Go and needs nothing installed, and can be switched to shell out to the git binary when go-git isn't enough (some `git apply` and `git am` edge cases, credential helpers).

## Auth

Credentials can be set once at the provider level and overridden per resource, which is what you want for a config spanning several hosts or accounts.
GitHub and GitLab first.

## Patch semantics

Patches become real commits on top of the tracked branch's base ref, not uncommitted working-tree changes.
Applying, reordering, or removing a patch rewrites those commits, the same way quilt treats the stack as mutable and the base as stable.

## Read behavior

Refresh updates the refs in state to whatever the remote actually has: the base ref for `git_branch`, and the resolved ref after the stack for the branch overall.
That's what makes drift visible to `terraform plan` when someone pushes or force-pushes behind Terraform's back.

## Conflict handling

When the stack stops applying cleanly, because the base ref moved upstream or a patch went stale, `git_branch` picks between two behaviors:

- **Fail**: error out with the conflict details and let the user sort it out.
- **Force**: reset the branch to the tracked base ref and reapply the whole stack from scratch, throwing away drift so the declared state wins.

## Edge cases: remote changes between runs

`git_branch` re-resolves both `base_ref` and the branch tip against the live remote on every `Read` (see Read behavior above), so each of these needs a deliberate answer rather than whatever the diff happens to show.

- **`base_ref` moves upstream (fast-forward or rewrite)**: `Read` takes the new `base_sha`, no questions asked. A fast-forward and a force-pushed rewrite look identical from here; both are just "the sha changed."
- **`base_ref` deleted upstream, no patches configured**: read as the resource being gone. State is dropped silently, with no diagnostic explaining why.
- **`base_ref` deleted upstream, patches configured**: dropping state silently isn't safe here, since the tracked branch tip can outlive `base_ref`, so this is a hard error rather than a warning or drift.
- **Branch tip deleted upstream** (someone deleted the branch on the host): always read as the resource being gone. State is dropped, and the next `apply` recreates the branch and re-pushes the stack without asking first.
- **Branch tip changed upstream to something unrelated to the stack** (a manual push, another tool, another Terraform run): `resolved_ref` picks up the real remote tip on `Read`. What that does to the plan, and whether `Update` corrects it or force-pushes over it, still needs checking against real backend behavior. This is exactly the case "Force" mode above is meant to own.
- **Concurrent force-push race**: `on_conflict = "fail"` closes this one. `Update` hands the backend the tip it last saw on `Read` (`resolved_ref`) as a compare-and-swap guard on push (`--force-with-lease`, or go-git's `ForceWithLease`), so a branch that moved under you since `Read` aborts with a conflict instead of getting clobbered. `on_conflict = "force"` (the default) keeps pushing unconditionally. The gap between the check and the push is inherent to `--force-with-lease` and doesn't go away, but it's much narrower than not checking at all.
- **Auth revoked or expired between `Read` and `Update`**: this must never look like a missing ref, or it would delete state.
  `Read` classifies by error *type*, not by message text.
  The `refNotFoundError` that triggers state removal is only built after `LsRemote` has already succeeded and the ref is genuinely missing from the list it returned, so any `LsRemote` failure at all (auth, network, transport) comes back as a diagnostic no matter what it says.
  That distinction earns its keep: a revoked GitHub token reports `remote: Repository not found`, which reads exactly like a missing ref.
  On the push side, the exec backend's lease-rejection check matches git's client-side `! [rejected]` line (the `--force-with-lease` check failing) and deliberately not the server-side `! [remote rejected]` line, so a permission denial or a declined hook surfaces as an error instead of a bogus compare-and-swap conflict telling the user to re-run apply.

## Push behavior

Once the stack is reconciled, `git_branch` pushes the branch to the remote.
The stack gets rewritten on every apply, so that push is a force-push.

## Remote patch sources

A `git_patch` from a host uses a host-specific nested block rather than a generic URL string or flat type/ref attributes:

```hcl
data "git_patch" "example" {
  github = {
    repository = "owner/name"
    pr         = 123
    # or: commit = "abc123"
  }
}

data "git_patch" "example_gitlab" {
  gitlab = {
    project = "group/project"
    mr      = 123
    # or: commit = "abc123"
  }
}
```

Validation stays host-aware and type-safe this way, and each host can expose its own fields (a GitLab MR wants different ones than a GitHub PR) without cramming them into a shared schema.
Supporting a new host means adding a new block type.

## Import behavior

Import writes down what the remote actually shows, the base ref and resolved ref, and makes no attempt to reverse-engineer a patch stack from the existing commits.
It doesn't fail on divergent commits, and it doesn't invent `git_patch` data sources for them.
Your config is what declares the intended stack, so the `terraform plan` right after an import shows the usual gap between observed and declared state, and the next apply reconciles it like any other drift.
That's the normal Terraform bargain: import fills in state, config decides the target.

## Workdir lifecycle

Local-clone workdirs are throwaway: a fresh clone into a temp directory per apply or read, deleted afterward.
Nothing persists, nothing is reused between runs, and there's no cache to invalidate or concurrent-run collision to reason about.
The cost is re-cloning every run; the payoff is no stale-state bugs.
