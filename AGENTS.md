# AGENTS.md

This file provides guidance to AI Agents when working with code in this repository.

## What this is

`terraform-provider-git` is a Terraform provider (HashiCorp `terraform-plugin-framework`) that declares and reconciles the state of a git repository: tracked branches and a quilt-style ordered patch stack applied on top of them.
See [GOALS.md](GOALS.md) for the vision/non-goals and [docs/DESIGN.md](docs/DESIGN.md) for the full resource model, backends, auth, conflict handling, and push/import semantics before implementing any resource logic — the design doc is the source of truth for intended behavior, and most of it is not yet implemented (see Current state below).

## Commands

Dev shell is Nix-managed (`flake.nix`, direnv auto-loads it via `.envrc`). Everything below assumes that shell (`GO`, `GOMOD2NIX`, `GINKGO` env vars point at Nix store binaries).

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

Only `git_repository` exists so far, as a data source (`internal/provider/git_repository_data_source.go`).
Its `Read` mirrors `url` into `id` and verifies the repository is reachable via `LsRemote` on the configured `git_implementation` backend, hard-erroring on failure.
Auth is resolved from the `auth.token` attribute and passed through to the backend (go-git or exec), see `internal/git`.

Not yet implemented (per DESIGN.md): `git_branch`, `git_patch`, the pluggable local-clone/hosting-API access backends, the pluggable go-git/shell-git backend, and GitHub/GitLab auth resolution. When implementing these, follow the terminology and semantics already fixed in DESIGN.md (base ref, resolved ref, patch stack, force-push-on-apply, ephemeral per-run workdirs) rather than inventing new ones.
