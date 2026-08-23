# Goals

`terraform-provider-git` treats a git repository the way other providers treat cloud infrastructure: you describe the state you want, and it converges the repository toward that.

## Vision

Terraform owns a repository's branches and the patch stack sitting on top of them, quilt-style.
Long-lived branches and patch series are tedious to keep by hand, so the intent lives in HCL and `terraform apply` does the bookkeeping.

## What it does

- Points at a repository that already exists.
- Tracks a branch against an upstream ref.
- Tracks an ordered stack of patches applied on top of that branch.
- Records the ref it actually observed on every read, so drift shows up in `terraform plan`.
- Authenticates against hosts like GitHub and GitLab.

## Non-goals

- Creating or deleting repositories on a host. That's what `github_repository` and friends are for.
- Host-specific settings, permissions, or webhooks.
- Serving as a long-term archive for patches.

Resource shapes, backends, and reconciliation details are in [docs/DESIGN.md](docs/DESIGN.md).
