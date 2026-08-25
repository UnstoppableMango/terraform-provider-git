// Package git defines the pluggable git access backend abstraction used by
// the provider to talk to remote git repositories.
package git

import (
	"context"
	"fmt"
	"strings"
)

// Auth carries authentication details used to connect to a remote
// repository. An empty Auth means an unauthenticated request.
type Auth struct {
	Token string // empty means unauthenticated
	Host  string // host type, e.g. "github" or "gitlab"; "" for unknown/generic
}

// Username returns the conventional HTTP basic-auth username to pair with a
// token for the given host type. Backends should use this instead of
// hardcoding a single convention, since it differs per host.
func Username(host string) string {
	switch host {
	case "gitlab":
		return "oauth2"
	default:
		return "x-access-token"
	}
}

// NormalizeURL rewrites an SSH remote URL to its https equivalent, covering
// both the scp-like form (git@host:owner/repo.git) and ssh://git@host/owner/
// repo.git. The bool reports whether url was rewritten; any other URL is
// returned unchanged.
//
// Callers use this for URLs read out of a local repository's config, where an
// SSH remote is the norm but token auth needs https.
func NormalizeURL(url string) (string, bool) {
	if rest, ok := strings.CutPrefix(url, "ssh://"); ok {
		// Strip any user@ prefix, then a :port if present: neither carries
		// over to https.
		if _, after, found := strings.Cut(rest, "@"); found {
			rest = after
		}

		host, path, found := strings.Cut(rest, "/")
		if !found {
			return url, false
		}
		if h, _, hasPort := strings.Cut(host, ":"); hasPort {
			host = h
		}

		return "https://" + host + "/" + path, true
	}

	if strings.Contains(url, "://") {
		return url, false
	}

	// scp-like syntax: [user@]host:path, where path is never absolute (that
	// would make it a local path with a colon in it, not a remote).
	hostPart, path, found := strings.Cut(url, ":")
	if !found || path == "" || strings.HasPrefix(path, "/") {
		return url, false
	}

	host := hostPart
	if _, after, hasUser := strings.Cut(hostPart, "@"); hasUser {
		host = after
	}
	if host == "" || strings.Contains(host, "/") {
		return url, false
	}

	return "https://" + host + "/" + path, true
}

// HostFromURL maps a repository URL's hostname to one of the host types the
// provider knows about: "github", "gitlab", or "generic". Hostnames beginning
// with "github." or "gitlab." are treated as self-hosted instances of those
// products. Anything unrecognized, including a URL this can't parse, is
// "generic".
func HostFromURL(url string) string {
	normalized, _ := NormalizeURL(url)

	rest, ok := strings.CutPrefix(normalized, "https://")
	if !ok {
		if rest, ok = strings.CutPrefix(normalized, "http://"); !ok {
			return "generic"
		}
	}

	host, _, _ := strings.Cut(rest, "/")
	if _, after, found := strings.Cut(host, "@"); found {
		host = after
	}
	if h, _, found := strings.Cut(host, ":"); found {
		host = h
	}
	host = strings.ToLower(host)

	switch {
	case host == "github.com" || strings.HasPrefix(host, "github."):
		return "github"
	case host == "gitlab.com" || strings.HasPrefix(host, "gitlab."):
		return "gitlab"
	default:
		return "generic"
	}
}

// Ref represents a single ref reported by a remote repository.
type Ref struct {
	Name string // e.g. "HEAD", "refs/heads/main"
	Hash string // full object hash
}

// ApplyPatchesRequest describes a patch stack to apply on top of a base ref
// and push to a branch on a remote.
type ApplyPatchesRequest struct {
	URL     string
	Auth    Auth
	Branch  string   // branch name to force-push the result to
	BaseRef string   // commit hash to start from
	Patches []string // ordered raw diff content, applied in order

	// ExpectedTip, when non-empty, is the branch's remote tip as last
	// observed by the caller (e.g. a prior Read). The push is performed as
	// a compare-and-swap: it only succeeds if the branch's current remote
	// tip still equals ExpectedTip, and returns a *ConflictError otherwise.
	// An empty ExpectedTip means push unconditionally, as before this field
	// existed (used for Create, and for Update when on_conflict is
	// "force").
	ExpectedTip string
}

// ApplyPatchesResult is the outcome of a successful ApplyPatches call.
type ApplyPatchesResult struct {
	ResolvedSHA string // HEAD after applying all patches, i.e. what was pushed
}

// ConflictError indicates a compare-and-swap push (see
// ApplyPatchesRequest.ExpectedTip) was rejected because the branch's actual
// remote tip no longer matched ExpectedTip: something else moved the branch
// between the caller's last observation and this push.
type ConflictError struct {
	Branch      string
	ExpectedTip string
	Err         error // underlying backend error, for detail/logging
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("branch %q has moved since it was last observed at %s: %v", e.Branch, e.ExpectedTip, e.Err)
}

func (e *ConflictError) Unwrap() error {
	return e.Err
}

// Commit identity used for commits the provider creates while applying a
// patch stack.
const (
	CommitAuthorName  = "terraform-provider-git"
	CommitAuthorEmail = "terraform-provider-git@localhost"
)

// Client is the pluggable git access backend. Implementations must treat an
// empty Auth as an unauthenticated request.
type Client interface {
	// LsRemote lists refs on the given remote URL, verifying it is reachable
	// and that auth (if any) is valid.
	LsRemote(ctx context.Context, url string, auth Auth) ([]Ref, error)

	// ApplyPatches applies req.Patches in order as commits on top of
	// req.BaseRef, then force-pushes the result to req.Branch on req.URL.
	ApplyPatches(ctx context.Context, req ApplyPatchesRequest) (ApplyPatchesResult, error)

	// IsAncestor reports whether ancestor is an ancestor of (or equal to)
	// descendant in url's history. A false, nil result means "no" — either
	// genuinely not an ancestor, or ancestor could no longer be found in
	// url's history even after a full fetch (rewritten away / garbage
	// collected), which for this provider's purposes is the same
	// conclusion: the ref did not move forward from ancestor. A non-nil
	// error means the check itself could not be completed (network, auth,
	// transport) and should be surfaced like any other backend failure.
	IsAncestor(ctx context.Context, url string, auth Auth, ancestor, descendant string) (bool, error)
}
