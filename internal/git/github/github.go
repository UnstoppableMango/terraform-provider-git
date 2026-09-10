// Package github defines the client used to resolve a git_patch's github
// source (a pull request or commit) against the GitHub REST API. It is a thin
// wrapper around github.com/google/go-github so tests can inject a fake.
package github

import "context"

// Resolution is the result of resolving a pull request or commit to a
// concrete commit SHA and unified diff.
type Resolution struct {
	SHA  string
	Diff string
}

// Client resolves github patch sources against the GitHub API. An empty token
// means an unauthenticated request.
type Client interface {
	// ResolvePR resolves a pull request number in repository (in "owner/name"
	// form) to its head commit SHA and the pull request's unified diff.
	ResolvePR(ctx context.Context, repository string, pr int64, token string) (Resolution, error)

	// ResolveCommit resolves a commit sha in repository (in "owner/name"
	// form) to its diff.
	ResolveCommit(ctx context.Context, repository, sha, token string) (Resolution, error)
}
