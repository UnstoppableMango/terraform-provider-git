# AGENTS.md

This file provides guidance to AI Agents when working with code in this repository.

## What this is

`terraform-provider-git` is a Terraform provider (HashiCorp `terraform-plugin-framework`) that declares and reconciles the state of a git repository: tracked branches and a quilt-style ordered patch stack applied on top of them.
See [GOALS.md](GOALS.md) for the vision/non-goals and [docs/DESIGN.md](docs/DESIGN.md) for the full resource model, backends, auth, conflict handling, and push/import semantics before implementing any resource logic — the design doc is the source of truth for intended behavior; see Current state below for the subset that is implemented.

## Commands

Dev shell is Nix-managed (`flake.nix`, direnv auto-loads it via `.envrc`). Everything below assumes that shell (`go`, `gomod2nix`, and `ginkgo` are available directly on `PATH`).

- Build: `make build` (wraps `nix build .#`)
- Test (all): `make test` (wraps `ginkgo run -r`)
- Test (single package): `ginkgo run ./internal/provider`
- Test (single spec): use Ginkgo focus, e.g. `ginkgo run --focus "<Describe/It text>" ./internal/provider`
- Lint/check: `make check` or `make lint` (wraps `nix flake check`)
- Format: `make fmt` or `make format` (wraps `nix fmt`, runs treefmt: gofmt, nixfmt, actionlint)
- Regenerate `go.sum` and `nix/gomod2nix.toml` after touching `go.mod`: `make tidy`

## Architecture

- Tests use Ginkgo/Gomega, not plain `testing.T` table tests. Every package with tests has a `TestX(t *testing.T)` entrypoint that calls `RegisterFailHandler(Fail)` + `RunSpecs` (see `main_suite_test.go`, `internal/provider/provider_suite_test.go`); actual specs live in `Describe`/`It` blocks.
- `main.go` just calls `providerserver.Serve` with `provider.New` — all provider logic lives under `internal/provider/`.
- `internal/provider/provider.go` defines `gitProvider`, the top-level `provider.Provider` implementation, and registers resources in `Resources()`. New resources get added there.
- Each resource is one file named `git_<resource>_resource.go` implementing `resource.Resource` (+ `resource.ResourceWithImportState` where import is supported), following the standard terraform-plugin-framework shape: a `<name>Resource` struct, a `<name>ResourceModel` with `tfsdk` tags, and `Metadata`/`Schema`/`Create`/`Read`/`Update`/`Delete` methods.

## Current state vs. design

Implemented, per entity:

- `git_repository` (`internal/provider/git_repository_data_source.go`): a data source matching DESIGN.md. `Read` mirrors `url` into `id` and verifies the repository is reachable via `LsRemote` on the configured `git_implementation` backend, adding a warning on failure. Auth is resolved from the `auth.token` attribute and passed through to the backend (go-git or exec), see `internal/git`.
- `git_branch` (`internal/provider/git_branch_resource.go`): a resource implementing base-ref tracking only. `Create`/`Update`/`Read` resolve `base_ref` against the repository's remote refs (exact name, then `refs/heads/`, then `refs/tags/`) and record `base_sha`/`resolved_ref`; `Delete` is a no-op. `ImportState` accepts `<url>#<name>`. The patch stack, push, force-push, and conflict handling in DESIGN.md are absent.
- `git_patch` (`internal/provider/git_patch_data_source.go`): a data source matching DESIGN.md's description of it as a read-only resolver. Resolves `diff` and a content-addressed `id` (sha256 of the diff) from exactly one of `content`, `file`, or `github` (GitHub PR/commit via `internal/git/github`). It has no apply/commit/push behavior and is not referenced from `git_branch`'s patch list, since that list is absent.

`git_branch`'s `patches` attribute and patch application/force-push (via `git.Client.ApplyPatches`, both the go-git and exec backends) are implemented.

Absent: provider-level auth inheritance (DESIGN.md describes credentials settable at the provider level and overridden per-resource; only per-resource `auth.token` exists today), conflict handling modes (Fail vs Force), the GitLab hosting-API backend (only GitHub patch resolution exists; `host: "gitlab"` validates but has no backing API client), and generated Registry documentation (no `tfplugindocs` wiring). When implementing these, follow the terminology and semantics already fixed in DESIGN.md (base ref, resolved ref, patch stack, force-push-on-apply, ephemeral per-run workdirs) rather than inventing new ones.
