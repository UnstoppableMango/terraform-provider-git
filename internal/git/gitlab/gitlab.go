// Package gitlab defines the client used to resolve a git_patch's gitlab
// source (a merge request or commit) against the GitLab REST API. It is a
// thin wrapper around gitlab.com/gitlab-org/api/client-go so tests can
// inject a fake.
package gitlab

import "context"

// Resolution is the result of resolving a merge request or commit to a
// concrete commit SHA and unified diff.
type Resolution struct {
	SHA  string
	Diff string
}

// Client resolves gitlab patch sources against the GitLab API. An empty
// token means an unauthenticated request.
type Client interface {
	// ResolveMR resolves a merge request IID in project (a GitLab project
	// path, e.g. "group/subgroup/project", or a numeric project ID as a
	// string) to its head commit SHA and a unified diff synthesized from
	// its changed files.
	ResolveMR(ctx context.Context, project string, mr int64, token string) (Resolution, error)

	// ResolveCommit resolves a commit sha in project to a unified diff
	// synthesized from its changed files.
	ResolveCommit(ctx context.Context, project, sha, token string) (Resolution, error)
}
