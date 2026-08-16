// Package github defines the client used to resolve a git_patch's github
// source (a pull request or commit) against the GitHub REST API.
package github

import "context"

// Auth carries authentication details used to call the GitHub API. An empty
// Auth means an unauthenticated request.
type Auth struct {
	Token string // empty means unauthenticated
}

// Resolution is the result of resolving a pull request or commit to a
// concrete commit SHA and unified diff.
type Resolution struct {
	SHA  string
	Diff string
}

// Client resolves github patch sources against the GitHub API.
type Client interface {
	// ResolvePR resolves a pull request number in repository (in "owner/name"
	// form) to its head commit SHA and diff.
	ResolvePR(ctx context.Context, repository string, pr int64, auth Auth) (Resolution, error)

	// ResolveCommit resolves a commit sha in repository (in "owner/name"
	// form) to its diff.
	ResolveCommit(ctx context.Context, repository string, sha string, auth Auth) (Resolution, error)
}
