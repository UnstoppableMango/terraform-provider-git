---
page_title: "Patch Stacks"
subcategory: ""
description: |-
  How git_branch's patch stack model works, and how to handle conflicts with on_conflict.
---

# Patch Stacks

`git_branch` can track more than a branch's base ref: setting `patches` declares an ordered stack of diffs applied on top of that base ref, in the spirit of [quilt](https://en.wikipedia.org/wiki/Quilt_(software))'s `quilt push`.

## The model

- `patches` is applied as real commits on top of `base_ref`, not as uncommitted working-tree changes.
- The stack is rewritten from `base_ref` on every `apply`: reordering, adding, or removing entries in `patches` produces a new set of commits, not an incremental edit of the old ones.
- Because the stack is rewritten each time, publishing it is always a force-push to the branch on the remote.
- `resolved_ref` (computed) reflects the commit the branch actually points to after the stack is applied; `base_sha` (computed) reflects `base_ref` alone.

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

`git_patch` resolves a single diff, from inline `content`, a local `file`, a GitHub PR/commit, or a GitLab MR/commit; it never applies, commits, or pushes anything itself.

## Conflicts: `on_conflict`

Every `Read` re-resolves `base_ref` and the branch's tip against the live remote, so the declared patch stack can conflict with what's actually there: `base_ref` may have moved, or another writer may have pushed to the branch since the last `Read`. `on_conflict` controls what happens when the stack is (re)applied:

- **`force`** (default): reset the branch to the tracked `base_ref` and reapply the full patch stack from scratch, discarding drift. This matches the provider's original, always-clobber behavior.
- **`fail`**: abort the push instead of clobbering an unexpected remote change. The branch tip last observed on `Read` is passed to the backend as a compare-and-swap guard (`--force-with-lease` / go-git's `ForceWithLease`), so a branch moved by someone else since the last `Read` aborts with a conflict diagnostic rather than being silently overwritten. Re-run `terraform apply` to pick up the new tip, or resolve the drift manually.

`on_conflict` only takes effect when `patches` is set; a `git_branch` with no patch stack never pushes.

## Destroying a `git_branch`

`git_branch` does not own branch existence on the remote, only its base ref and patch stack. `terraform destroy` is therefore a no-op against the remote: it only removes the resource from Terraform state, leaving the branch and any pushed patches in place.

## Further reading

- [`docs/DESIGN.md`](https://github.com/UnstoppableMango/terraform-provider-git/blob/main/docs/DESIGN.md) covers the full resource model, including edge cases around `base_ref` and branch-tip drift between runs.
- [`examples/full/github`](https://github.com/UnstoppableMango/terraform-provider-git/tree/main/examples/full/github) and [`examples/full/gitlab`](https://github.com/UnstoppableMango/terraform-provider-git/tree/main/examples/full/gitlab) are complete end-to-end configurations.
