# AGENTS.md

This file provides guidance to AI Agents when working with code in this repository.

## What this is

`terraform-provider-git` is a Terraform provider (HashiCorp `terraform-plugin-framework`) that declares and reconciles the state of a git repository: tracked branches and a quilt-style ordered patch stack applied on top of them.
See [GOALS.md](GOALS.md) for the vision/non-goals and [docs/DESIGN.md](docs/DESIGN.md) for the full resource model, backends, auth, conflict handling, and push/import semantics before implementing any resource logic — the design doc is the source of truth for intended behavior; see Current state below for the subset that is implemented.

## Commands

Dev shell is Nix-managed (`flake.nix`, direnv auto-loads it via `.envrc`). Everything below assumes that shell (`go` and `gomod2nix` are available directly on `PATH`). Ginkgo is a go tool dependency (`go.mod` `tool` directive) instead, invoked as `go tool ginkgo` so the version tracks `go.mod`, not nixpkgs.

- Build: `make build` (wraps `nix build .#`)
- Test (all): `make test` (wraps `go tool ginkgo run -r`)
- Test (single package): `go tool ginkgo run ./internal/provider`
- Test (single spec): use Ginkgo focus, e.g. `go tool ginkgo run --focus "<Describe/It text>" ./internal/provider`
- Lint/check: `make check` or `make lint` (wraps `nix flake check`)
- Format: `make fmt` or `make format` (wraps `nix fmt`, runs treefmt: gofmt, nixfmt, actionlint)
- Generate Registry docs: `make docs` or `make generate` (wraps `tfplugindocs generate`, also invocable via `go generate ./...` per the directive in `main.go`)
- Acceptance tests (hits real backends): `make test-acc` (`TF_ACC=1 go test ./...`)
- Regenerate `go.sum` and `nix/gomod2nix.toml` after touching `go.mod`: `make tidy`

## Architecture

- Tests use Ginkgo/Gomega, not plain `testing.T` table tests. Every package with tests has a `TestX(t *testing.T)` entrypoint that calls `RegisterFailHandler(Fail)` + `RunSpecs` (see `main_suite_test.go`, `internal/provider/provider_suite_test.go`); actual specs live in `Describe`/`It` blocks.
- `main.go` just calls `providerserver.Serve` with `provider.New` — all provider logic lives under `internal/provider/`.
- `internal/provider/provider.go` defines `gitProvider`, the top-level `provider.Provider` implementation, and registers resources in `Resources()`. New resources get added there.
- Each resource is one file named `git_<resource>_resource.go` implementing `resource.Resource` (+ `resource.ResourceWithImportState` where import is supported), following the standard terraform-plugin-framework shape: a `<name>Resource` struct, a `<name>ResourceModel` with `tfsdk` tags, and `Metadata`/`Schema`/`Create`/`Read`/`Update`/`Delete` methods.

## Current state vs. design

Implemented, per entity:

- `git_repository` (`internal/provider/git_repository_data_source.go`): a data source matching DESIGN.md. `Read` mirrors `url` into `id` and verifies the repository is reachable via `LsRemote` on the configured `git_implementation` backend, adding a warning on failure. Auth is resolved from the `auth.token` attribute, falling back to the provider-level `auth.token` default when unset, and passed through to the backend (go-git or exec), see `internal/git`. Exactly one of `url` or `local` is set: `local` discovers the repository from a checkout on disk via `internal/git/local` (identity only, see DESIGN.md's "Current repository"), filling in `url`, the computed `local.*` observations, and `host` when the config leaves it unset (`git.HostFromURL`). An SSH remote URL is rewritten to https (`git.NormalizeURL`) only when a token is available.
- `git_branch` (`internal/provider/git_branch_resource.go`): a resource implementing base-ref tracking, the patch stack, and force-push. `Create`/`Update`/`Read` resolve `base_ref` against the repository's remote refs (exact name, then `refs/heads/`, then `refs/tags/`) and record `base_sha`/`resolved_ref`; `Delete` is a no-op. `ImportState` accepts `<url>#<name>`. `patches` and patch application/force-push (via `git.Client.ApplyPatches`, both the go-git and exec backends) are implemented, as is `on_conflict` (`fail` vs `force`, DESIGN.md's conflict handling modes) via a compare-and-swap push guard. `repository.head_ref` is advisory only: it drives the self-write warning in `Create`/`Update` (`checkSelfWrite`) and nothing else.
- `git_patch` (`internal/provider/git_patch_data_source.go`): a data source matching DESIGN.md's description of it as a read-only resolver. Resolves `diff` and a content-addressed `id` (sha256 of the diff) from exactly one of `content`, `file`, `github` (GitHub PR/commit via `internal/git/github`), or `gitlab` (GitLab MR/commit via `internal/git/gitlab`), with the GitHub/GitLab API token falling back to the provider-level `auth.token` default when unset. It has no apply/commit/push behavior.

Provider-level auth inheritance is implemented: `provider.go`'s `auth.token` attribute is stored as `providerData.DefaultToken` and used by `git_repository`, `git_branch`, and `git_patch` (including `git_branch`'s `ImportState`, which has no resource config to read a per-resource token from) whenever their own `auth.token` is unset, via the shared `tokenFromModel`/`authFromModel` helpers in `git_repository_data_source.go`.

The GitLab hosting-API backend (`internal/git/gitlab`) is implemented, mirroring `internal/git/github`'s shape (a small `Client` interface plus a real implementation wrapping `gitlab.com/gitlab-org/api/client-go`). One notable asymmetry: GitLab's REST API has no single endpoint that returns a ready-to-use unified diff the way GitHub's raw-diff endpoint does — merge request/commit diffs come back as a paginated array of per-file objects whose `diff` field holds only the `@@ ...@@` hunk body, so `internal/git/gitlab/client.go` reconstructs the `diff --git`/`---`/`+++` header lines itself (`renderUnifiedDiff`). This targets the common case (added/modified/deleted text files) correctly; renamed files get best-effort `rename from`/`rename to` headers, and binary files (whose `diff` field GitLab typically leaves empty) are not specially handled — a real, documented behavioral gap versus the `github` backend, not an oversight.

Generated Registry documentation is wired: `docs/index.md`, `docs/resources/branch.md`, `docs/data-sources/{repository,patch}.md` are produced by `tfplugindocs generate` (`make docs`, or the `//go:generate` directive in `main.go`) from each resource's `Schema` `MarkdownDescription`s — regenerate after changing any schema description, don't hand-edit the `<!-- schema generated by tfplugindocs -->` sections.
