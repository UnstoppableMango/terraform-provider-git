// Package git defines the pluggable git access backend abstraction used by
// the provider to talk to remote git repositories.
package git

import "context"

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

// Ref represents a single ref reported by a remote repository.
type Ref struct {
	Name string // e.g. "HEAD", "refs/heads/main"
	Hash string // full object hash
}

// Client is the pluggable git access backend. Implementations must treat an
// empty Auth as an unauthenticated request.
type Client interface {
	// LsRemote lists refs on the given remote URL, verifying it is reachable
	// and that auth (if any) is valid. Returns an error if the remote
	// cannot be listed.
	LsRemote(ctx context.Context, url string, auth Auth) ([]Ref, error)
}
