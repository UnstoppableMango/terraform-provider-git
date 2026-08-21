# Roadmap

This traces the project from its first commit through the current state (branch `gitlab-backend`, 2026-08-21), then lists what's still open against [GOALS.md](../GOALS.md) and [DESIGN.md](DESIGN.md).
It supersedes no other doc; DESIGN.md remains the source of truth for intended behavior, and AGENTS.md's "Current state vs. design" section remains the source of truth for what's implemented.
This file exists to give the arc: where the provider came from, and where it's headed.

## History

### Phase 0: Bootstrap (2026-08-13)

- `61c49ed` Initial commit, `c4cde6d` init, `dee2441` git repo.
  Scaffolding for a `terraform-plugin-framework` provider.

### Phase 1: `git_repository` (2026-08-15 to 2026-08-16)

- `561fca9` git_repository implementation.
- `0bb0792` Convert git_repository into a data source.
  Established the pattern that repository identity/reachability is a read-only data source, not a managed resource, per GOALS.md's non-goal of creating/deleting host repositories.

### Phase 2: `git_patch` and `git_branch` (2026-08-16 to 2026-08-17)

- `6239e82` Patch data source.
- `651e2f2` Switch to go-github.
- `d4fc596` git_branch.
  Introduced the two core building blocks: `git_patch` resolving diff content/identity, `git_branch` as the stateful resource tracking a base ref and patch list.

### Phase 3: Patch application (2026-08-18)

- `674a52b` PR comment, `3b7fe3f` examples, `cef2948` Update design docs for resource changes.
- `85827eb` apply patches, `01a3918` in proc apply patches.
  This is where the quilt-style "apply the patch stack as real commits on top of base_ref" behavior described in DESIGN.md's "Patch semantics" landed.

### Phase 4: Tooling and CI hardening (2026-08-18 to 2026-08-19)

- `957f479` Flake update, `ecf39aa` gh stack agent skill, `fbbd4c1` TF code review, `3af70b3` Switch to ginkgo as a go tool, `1ad1b36` Update go, `8e82e7d` GitHub output, `7b74a7b` Set GitHub token for acceptance tests, `37cbd1d` More formatters, `9839027` Fix agent skills, `10c5707` Run CI checks on PRs against any base branch.
  Not feature work, but it's why `make test`/`make check`/`make fmt` and Ginkgo-as-a-go-tool are reliable today.

### Phase 5: DESIGN.md's edge cases, one by one (2026-08-19)

DESIGN.md's "Edge cases: remote changes between runs" section reads today like a checklist, and this phase is where most of it got checked off:

- `44e2113` Edge cases, `621626a` Edge case 2, `b700168` Edge case 3.
- `37a8335` Upstream tip deleted: branch tip vanishing is treated as the resource being gone (state removed, warning surfaced); `base_ref` vanishing is only a silent removal when no patches are configured, otherwise a hard error, since the tracked tip may still exist independent of `base_ref`.
- `4858429` Detect and warn on non-fast-forward base_ref changes: added `IsAncestor` to the `git.Client` interface (both `execgit` and `gogit`), wired into `git_branch`'s `Read` via `checkBaseRefAncestry`, so a force-pushed rewrite of `base_ref` now produces a distinct warning from an ordinary fast-forward.
  This closes the item my earlier gap analysis flagged as open, and DESIGN.md's prose describing it as unresolved is now stale.
- `8df2282` Force changes on conflict: added `on_conflict` (`fail`/`force`) with a compare-and-swap push guard (`ExpectedTip` / `--force-with-lease`), covering DESIGN.md's "Conflict handling" and "Concurrent force-push race" sections.

Also verified while writing this roadmap: the "ref not found" signal that triggers state removal is a typed `*refNotFoundError` from `resolveBranchRef`, not a string match on git's stderr, so an auth failure from `LsRemote` (a plain wrapped error) cannot be misclassified as a missing ref.
DESIGN.md's line asking to confirm this ("worth confirming the exec-backend's error strings can't accidentally match the ref-not-found heuristic") is satisfied by the current implementation and can be considered resolved.

### Phase 6: Provider auth and GitLab (2026-08-19 to 2026-08-21)

- `f12017c` Readme, `8df2282`/`37a8335` (listed above), `c3989b6` Add provider auth fallback support: provider-level `auth.token` now flows to `git_repository`, `git_branch` (including `ImportState`), and `git_patch` via shared `tokenFromModel`/`authFromModel` helpers.
- `634244f` GitLab backend: `internal/git/gitlab` mirrors `internal/git/github`'s shape for MR/commit diff resolution, with a documented gap around reconstructing unified diffs for binary and renamed files (GitLab's API doesn't return a ready-made diff the way GitHub's does).

### Phase 7: SSH auth (2026-08-21, current branch)

Closed the top item from this roadmap's first "Now / Next" list. `auth` gained a nested `ssh` block (private key content, a private key file path, or a locally running SSH agent), on `provider`, `git_repository`, and `git_branch`'s `repository.auth` — mirroring the existing `token` fallback pattern (a resource's own `ssh` block wins over the provider-level default, as a whole block, not merged field-by-field).

- `internal/git/git.go`'s `Auth` struct grew `SSHUser`/`SSHPrivateKey`/`SSHPrivateKeyPath`/`SSHPassphrase`/`SSHAgent` fields.
- `internal/git/gogit/client.go`'s `authMethod` now dispatches to `ssh.NewPublicKeys`/`NewPublicKeysFromFile`/`NewSSHAgentAuth`, fully supporting passphrase-protected keys (in-process key parsing).
- `internal/git/execgit/client.go`'s `gitEnv` wires up a `GIT_SSH_COMMAND` pointing at a small embedded wrapper script (mirroring the existing `askpass.sh` pattern, to avoid ever shell-interpolating a user-supplied key path). Passphrase-protected keys are **not** supported non-interactively by this backend; it returns a clear error rather than hanging.
- `git_patch` deliberately did not get an `ssh` block: its `auth` only authenticates GitHub/GitLab REST API calls, which have no SSH equivalent. It got its own smaller `gitPatchAuthModel` (token-only) to keep that boundary explicit at the type level, separate from the shared `gitRepositoryAuthModel` used everywhere else.
- Host key verification deliberately has no new schema surface: leaving go-git's `HostKeyCallback` unset already builds a `known_hosts`-based callback automatically (verified in go-git's vendored source), erroring clearly if none exists rather than silently bypassing verification.

### Phase 8: `tfplugindocs` wiring (2026-08-21)

Closed this roadmap's former "Now / Next" item 3.

- Added `tfplugindocs` (from nixpkgs, not a `go.mod` tool — unlike Ginkgo, it's never imported as a Go library, so there's no CLI/library version to keep in sync) to the dev shell, a `make docs` target, and a CI step that fails if `docs/` is stale (`tfplugindocs generate && git diff --exit-code -- docs/`).
- `docs/index.md`, `docs/resources/branch.md`, `docs/data-sources/{repository,patch}.md` are now generated from schema `MarkdownDescription`s and `examples/`; added `examples/resources/git_branch/import.sh` so `git_branch`'s import instructions render.
- `provider.go`'s top-level schema gained a `MarkdownDescription` (previously only its attributes had one), so `docs/index.md` isn't blank.
- `treefmt`'s `mdformat` corrupts tfplugindocs' YAML frontmatter (turns the `---` delimiters into a horizontal rule), so `docs/index.md`, `docs/resources/**`, and `docs/data-sources/**` are excluded from it in `flake.nix`, same as the existing skills excludes.

## Now / Next

Everything below is open relative to GOALS.md/DESIGN.md as of this branch.
Ordered roughly by how load-bearing it is for the provider's stated purpose, not by effort.

### 1. Hosting-API backend used only for read paths

DESIGN.md's "Access backends" section describes the local-clone and hosting-API backends as interchangeable per-operation, picked for whichever is correct/efficient.
In practice, the GitHub/GitLab API clients are only used for `git_patch` resolution and `git_repository`'s reachability check.
Patch application and push always go through the local-clone backend.
There's no hosting-API path for push (e.g. building commits/refs via GitHub's Git Data API without a local clone), which is one of the scenarios the pluggable-backend design was meant to enable.

### 2. GitLab diff reconstruction gaps

Already called out in AGENTS.md as a known, documented asymmetry: `internal/git/gitlab/client.go`'s `renderUnifiedDiff` handles added/modified/deleted text files correctly, gives renames best-effort headers, and doesn't specially handle binary files (GitLab's API typically leaves their `diff` field empty).
Worth closing before GitLab is presented as an equal peer to GitHub rather than a "mostly works" backend.

### 3. Host extensibility beyond GitHub/GitLab

GOALS.md phrases the initial hosts as examples ("auth for hosted providers such as GitHub and GitLab"), implying more may follow.
Right now each host is a hand-written package mirroring the previous one (`internal/git/github`, `internal/git/gitlab`); there's no shared scaffolding that would make adding e.g. Bitbucket or a self-hosted Gitea cheap.
Lower priority than the items above since nothing in GOALS.md commits to a specific third host.

### 3a. Branch tip changed upstream to something unrelated to the patch stack

DESIGN.md's edge-case list still poses this as an open question ("needs verifying against the actual backend behavior").
Given `on_conflict` now exists (`fail` compare-and-swaps against the last-observed tip; `force` unconditionally overwrites), this is very likely already covered as a side effect of Phase 5's work, but there's no test explicitly naming this scenario ("branch tip drifted to unrelated content, not just a stale patch stack").
Worth an explicit Ginkgo spec to confirm and let DESIGN.md's wording be updated, rather than new implementation work.

### Not roadmap items (explicit non-goals, per GOALS.md)

- Creating/deleting repositories on a host.
- Managing host-specific repository settings, permissions, or webhooks.
- Acting as a long-term patch archive.
