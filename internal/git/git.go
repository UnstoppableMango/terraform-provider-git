// Package git defines the pluggable git access backend abstraction used by
// the provider to talk to remote git repositories.
package git

import (
	"context"
	"fmt"
)

// Auth carries authentication details used to connect to a remote
// repository. An empty Auth means an unauthenticated request.
type Auth struct {
	Token string // empty means unauthenticated
	Host  string // host type, e.g. "github" or "gitlab"; "" for unknown/generic

	SSHUser           string // ssh username; empty defaults to "git" per-backend
	SSHPrivateKey     string // PEM-encoded private key content
	SSHPrivateKeyPath string // path to a private key file on disk
	SSHPassphrase     string // passphrase for an encrypted private key
	SSHAgent          bool   // use a locally running SSH agent instead of a key
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
