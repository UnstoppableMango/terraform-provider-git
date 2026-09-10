# Goals

`terraform-provider-git` declares and converges the desired state of a git repository, the way other providers declare the desired state of cloud infrastructure.

## Vision

Terraform manages a repository's branches and the patch stack applied on top of them, quilt-style.
Instead of hand-maintaining long-lived feature branches or juggling patch series by hand, the desired state lives in HCL and Terraform reconciles the repository to match.

## What it does

- References an existing git repository.
- Tracks a branch against an upstream ref.
- Tracks an ordered stack of patches applied on top of that branch.
- Updates state with the observed ref on every read, so drift shows up in `terraform plan`.
- Supports auth for hosted providers such as GitHub and GitLab.

## Non-goals

- Creating or deleting repositories on a host. Use the host's own provider (e.g. `github_repository`) for that.
- Managing host-specific repository settings, permissions, or webhooks.
- Acting as a long-term patch archive.

See [docs/DESIGN.md](docs/DESIGN.md) for resource shapes, backends, and reconciliation details.
